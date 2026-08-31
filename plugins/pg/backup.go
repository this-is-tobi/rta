package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/format"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The whole database, for a person, as a file.
//
// **This is the capability that has no nameable blast radius, and it is
// therefore the one that refuses MCP outright rather than asking for a
// grant.** Every other control in rta bounds a call by what it names —
// Scope narrows a grant to one record, --limit bounds a result, a profile
// bounds an environment — and a full dump's single authorized use is
// "everything". A grant that could only ever mean that is not consent, it is
// a rubber stamp with an expiry date. keys.backup and kv.copy draw the same
// line for the same reason, and leaving NeedsGrant unset is deliberate and
// copied from keys.backup: a grant that can never be exercised over the one
// surface grants exist to gate would be a standing entry in `grant list`
// that means nothing.
//
// So the whole-database dump exists, and it belongs to whoever is at the
// keyboard.
//
// **It shells out to pg_dump rather than reimplementing it**, which is the
// most important decision here. A restorable dump has to get sequences,
// extensions, ownership, row-level security, large objects and COPY escaping
// right, and a half-written pg_dump that produces a file which will not
// restore is worse than no capability at all — a backup you cannot restore
// is not a backup, it is a belief about one. builtin/kv sets the precedent
// for depending on a tool that is simply present or simply not (`pbcopy`,
// `xclip`), including naming it when it is missing.

// dumpTools are tried in order. pg_dump is the only real answer; the list
// exists so the "not installed" refusal can name what it looked for.
var dumpTools = []string{"pg_dump"}

// humanOnly is this plugin's copy of the gate builtin/keys opens with. It
// comes first in the handler, before the connection is opened, so an agent's
// call never spends the operator's password on a question that was always
// going to be answered no.
func humanOnly(req plugin.Request, id string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Errorf("pg.human", "%s can only be run by a person at a terminal", id).
		WithHint("a whole-database dump has no blast radius a grant could name — its one " +
			"authorized use is everything. Ask for the table you need with pg.table.dump, " +
			"which takes a grant naming that table")
}

func runFullDump(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "pg.dump"); verr != nil {
		return nil, verr
	}

	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("pg.dump.nooutput", "say where the dump should be written").
			WithHint("--out ./" + req.String("database") + backupSuffix(req.String("format")) +
				" — a whole database is a file, not something to read in a terminal")
	}
	path, err := expandHome(out)
	if err != nil {
		return nil, view.Errorf("pg.dump.path", "resolving --out: %v", err)
	}

	tool, err := lookupDumpTool()
	if err != nil {
		return nil, view.Errorf("pg.dump.missing", "no %s on $PATH",
			strings.Join(dumpTools, " or ")).
			WithHint("rta does not reimplement it: a dump has to get sequences, extensions, " +
				"ownership and COPY escaping right, and one that will not restore is worse " +
				"than none. Install the PostgreSQL client tools — `brew install libpq` or " +
				"`apt install postgresql-client`")
	}

	if verr := checkParallel(req); verr != nil {
		return nil, verr
	}
	// A friendly early refusal, before anything opens a connection to say the
	// same thing more slowly. It is not the guarantee — O_EXCL in writeDump
	// is, and that still catches the race this stat cannot.
	if _, err := os.Stat(path); err == nil {
		return nil, alreadyThere(path)
	}

	args := dumpArgs(req)
	if req.String("format") == "directory" {
		// Directory format is the only one pg_dump writes itself, so it takes
		// the destination rather than handing bytes back through a pipe. The
		// path is not a secret, so argv is fine for it — unlike the password,
		// which is why that rule is stated where the password is.
		args = append(args, "--file="+path)
	}
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would run %s %s\nand write %s",
			filepath.Base(tool), strings.Join(args, " "), path)}, nil
	}

	// **Ask the server what it is before dumping it.** Two things come back
	// that the receipt cannot honestly leave out — whether this is a primary
	// or a replica, and what version it is — and it fails here, in a
	// classified error, rather than after a file has been created and a child
	// process has started.
	src, verr := describeSource(ctx, req)
	if verr != nil {
		return nil, verr
	}

	started := time.Now()
	written, verr := writeDump(ctx, tool, args, req, path)
	if verr != nil {
		return nil, verr
	}

	return view.KeyValue{Pairs: []view.Pair{
		{Key: "wrote", Value: path},
		{Key: "size", Value: format.Bytes(uint64(written))},
		{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
		{Key: "contents", Value: contentsOf(req)},
		{Key: "source", Value: src.describe()},
		{Key: "consistency", Value: consistencyOf(req)},
		// Named on the answer rather than left in the docs. The file is every
		// row in the database in the clear, and the moment to say so is while
		// somebody is looking at where it landed.
		{Key: "at rest", Value: "unencrypted, mode 0600 — `rta kv` or `age` if it is going anywhere"},
		{Key: "restore with", Value: restoreCommand(req, path)},
	}}, nil
}

// source is what the server said it is when asked, just before the dump.
type source struct {
	role    string // "primary" or "standby"
	version int    // server_version_num, e.g. 170011
}

func (s source) standby() bool { return s.role == "standby" }

func (s source) describe() string {
	where := fmt.Sprintf("%s, PostgreSQL %d.%d", s.role, s.version/10000, s.version%10000)
	if s.standby() {
		// Said on the receipt because it changes what the dump means and what
		// can go wrong with it, and the moment to say so is while somebody is
		// looking at the backup they just took.
		where += " — a replica is as current as its replay lag, which `rta pg overview` reports"
	}
	return where
}

// describeSource asks the server what it is before anything is dumped from
// it, so a failure to reach it is a classified error rather than a child
// process exiting oddly after a file has already been created.
func describeSource(ctx context.Context, req plugin.Request) (source, *view.Error) {
	conn, verr := connect(ctx, req)
	if verr != nil {
		return source{}, verr
	}
	defer func() { _ = conn.Close(ctx) }()

	var s source
	role, err := roleOf(ctx, conn)
	if err != nil {
		return source{}, classify(err, req)
	}
	s.role = role
	if err := conn.QueryRow(ctx,
		`select current_setting('server_version_num')::int`).Scan(&s.version); err != nil {
		return source{}, classify(err, req)
	}
	return s, nil
}

// consistencyOf states the guarantee the dump actually carries.
//
// **pg_dump is consistent by default and it is worth saying how**, because
// the mechanism is what decides whether the parallel form is equally safe. A
// serial dump runs in one REPEATABLE READ transaction, so every table is
// read from a single snapshot no concurrent writer can move. A parallel dump
// cannot share one transaction across workers, so the leader exports its
// snapshot with pg_export_snapshot() and every worker joins it — the same
// point in time, from N connections.
//
// Which is exactly why **--no-synchronized-snapshots is never passed here**,
// not as a fallback and not on older servers. It is the one flag that turns a
// parallel dump into a set of unrelated reads at different times, producing a
// file that restores without complaint and holds a database state that never
// existed. If a server cannot export a snapshot, the right answer is the
// serial dump, and pg_dump saying so is a better outcome than rta silently
// dropping the guarantee to keep a flag working.
func consistencyOf(req plugin.Request) string {
	if n := req.Int("jobs"); n > 1 {
		return fmt.Sprintf("one snapshot shared by %d workers (pg_export_snapshot)", n)
	}
	return "one REPEATABLE READ snapshot"
}

// checkParallel refuses --jobs anywhere it would not work, by name.
//
// pg_dump parallelises by opening one connection per worker and having each
// dump a different table, which needs an output the workers can write
// independently — so it is directory format or nothing. pg_dump says
// "parallel backup only supported by the directory format" and exits; saying
// it here means the message names rta's own flags and arrives before a
// connection is opened.
//
// Refused rather than silently switching the format, which would hand
// somebody a directory where they asked for a file and change the restore
// command under them.
func checkParallel(req plugin.Request) *view.Error {
	if req.Int("jobs") <= 1 || req.String("format") == "directory" {
		return nil
	}
	return view.Errorf("pg.dump.notparallel",
		"--jobs needs --format directory, not %s", req.String("format")).
		WithHint("pg_dump parallelises by giving each worker its own connection and its own " +
			"file, so there has to be a directory to put them in — `--format directory " +
			"--jobs " + strconv.Itoa(req.Int("jobs")) + "`, restored with `pg_restore --jobs`")
}

// writeDump creates the destination, runs the tool, and reports how much
// landed — cleaning up whatever it made if the run fails.
//
// **The exclusive create is the no-overwrite guarantee**, one syscall rather
// than a stat followed by a create, which is a race a backup should not have.
// keys.restore refuses to write over an existing key for the same reason.
// 0600 for a file and 0700 for a directory, set at creation rather than
// chmod'd afterwards, so there is no instant where the dump is both complete
// and readable by everyone: this is every row in the database.
func writeDump(ctx context.Context, tool string, args []string,
	req plugin.Request, path string) (int64, *view.Error) {
	if req.String("format") == "directory" {
		// pg_dump writes the files, and accepts an existing directory only if
		// it is empty — so creating it here first both reserves the name
		// exclusively and hands the tool something it will take.
		//
		// Parents first, so `--out ./backups/2026-08-29` works without
		// pre-creating `backups`; then the leaf exclusively, which is where
		// the no-overwrite guarantee lives. MkdirAll on the leaf would accept
		// an existing directory and lose whatever was in it.
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return 0, view.Errorf("pg.dump.create", "creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.Mkdir(path, 0o700); errors.Is(err, os.ErrExist) {
			return 0, alreadyThere(path)
		} else if err != nil {
			return 0, view.Errorf("pg.dump.create", "creating %s: %v", path, err)
		}
		if verr := runDumpTool(ctx, tool, args, req, nil); verr != nil {
			_ = os.RemoveAll(path)
			return 0, verr
		}
		return sizeOnDisk(path), nil
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return 0, alreadyThere(path)
	}
	if err != nil {
		return 0, view.Errorf("pg.dump.create", "creating %s: %v", path, err)
	}
	verr := runDumpTool(ctx, tool, args, req, f)
	closeErr := f.Close()
	switch {
	case verr != nil:
		// A partial dump left on disk is the failure that gets restored six
		// months later, so the half-written file goes rather than staying to
		// be mistaken for a good one.
		_ = os.Remove(path)
		return 0, verr
	case closeErr != nil:
		_ = os.Remove(path)
		return 0, view.Errorf("pg.dump.write", "finishing %s: %v", path, closeErr)
	}
	return sizeOnDisk(path), nil
}

func alreadyThere(path string) *view.Error {
	return view.Errorf("pg.dump.exists", "%s already exists", path).
		WithHint("a dump is never written over an existing file — name a new one, or " +
			"move that one aside")
}

// sizeOnDisk measures what was written — one file, or every file under a
// directory-format dump.
//
// **It asks the filesystem how long the file is, rather than asking the
// descriptor where it got to**, and that distinction was a live bug: the
// first version read `f.Seek(0, io.SeekCurrent)` after the child exited, on
// the reasoning that the child inherits this exact descriptor and therefore
// shares its offset. It does. What that misses is that **pg_dump seeks** —
// custom format writes the archive and then goes back to patch the
// table-of-contents offsets, so the shared offset ends up near the start of
// the file. A 219 MB dump reported itself as 6.6 KiB, which is exactly the
// kind of wrong that looks plausible on a receipt nobody re-measures.
//
// Best effort: a size that could not be read is worth less than the dump it
// describes, and the dump is already safely on disk by the time this runs.
func sizeOnDisk(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	if !info.IsDir() {
		return info.Size()
	}
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a file that cannot be stat'd is not a failed backup
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// dumpArgs builds pg_dump's argv.
//
// **Never a shell string, and never the password.** argv is world-readable
// through `ps` on every platform this runs on, so the credential goes to the
// child through its environment and nowhere else; the same reason rta builds
// its own DSN rather than a URL is the reason this builds a slice rather
// than a command line.
func dumpArgs(req plugin.Request) []string {
	args := []string{
		"--host=" + req.String("host"),
		"--port=" + strconv.Itoa(req.Int("port")),
		"--username=" + req.String("user"),
		"--dbname=" + req.String("database"),
		// Without this, a missing password makes pg_dump prompt on a
		// terminal the plugin does not own, and the call hangs until
		// somebody kills it. Failing immediately is the behaviour a wrapper
		// owes its caller.
		"--no-password",
	}
	switch f := req.String("format"); f {
	case "custom", "directory":
		args = append(args, "--format="+f)
	default:
		args = append(args, "--format=plain")
	}
	// **The one flag that changes the transfer rate rather than the output.**
	// pg_dump with --jobs N opens N connections and dumps N tables at once,
	// which turns a serial walk of a big database into work the server and
	// the disk can overlap. checkParallel has already refused it anywhere it
	// would not apply, so reaching here means directory format.
	if n := req.Int("jobs"); n > 1 {
		args = append(args, "--jobs="+strconv.Itoa(n))
	}
	switch req.String("include") {
	case "schema":
		args = append(args, "--schema-only")
	case "data":
		args = append(args, "--data-only")
	}
	return args
}

// runDumpTool runs the child, writing to f when there is one.
//
// **When f is an *os.File, os/exec hands its descriptor to the child
// directly** — no pipe, no copying goroutine, no buffer in this process at
// all. That is the whole transfer path for a plain or custom dump: pg_dump
// writes to the destination fd and rta is not on the hot path. It is worth
// naming because the obvious alternative — StdoutPipe and io.Copy — would put
// every byte of a hundred-gigabyte database through a 32 KiB buffer in a Go
// process that has no reason to see any of it.
//
// Directory format passes nil: the tool writes its own files, which is what
// makes --jobs possible.
func runDumpTool(ctx context.Context, tool string, args []string,
	req plugin.Request, f *os.File) *view.Error {
	cmd := exec.CommandContext(ctx, tool, args...)
	if f != nil {
		cmd.Stdout = f
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Env = childEnv(req)

	if err := cmd.Run(); err != nil {
		return classifyDump(err, stderr.String(), req)
	}
	return nil
}

// childEnv is exactly what pg_dump needs and nothing else.
//
// A child inherits the whole environment otherwise, and this one is handed a
// database password: it gets what it needs to connect and no more, so an
// unrelated token in the operator's shell is not something pg_dump could ever
// have printed.
func childEnv(req plugin.Request) []string {
	// **LC_ALL=C because rta reads this output to classify it.** pg_dump is
	// translated, and the first live run of the replica-conflict path came
	// back as `pg_dump: détail : La commande était : COPY public.t1 ...` —
	// which matched only by luck, because the half that matched was the
	// server's message rather than pg_dump's own label. A classifier that
	// works in one locale and silently degrades to "unrecognised failure" in
	// another is worse than one that never worked, so the child's messages
	// are pinned to the language this code is written in. It does not change
	// the dump: format and encoding come from COPY and client_encoding, not
	// from LC_ALL.
	//
	// The server's own messages still arrive in the server's lc_messages,
	// which nothing here controls — the reason `classify` matches SQLSTATE
	// codes for the driver and only this path matches text at all.
	env := []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	if pw := req.String("password"); pw != "" {
		env = append(env, "PGPASSWORD="+pw)
	}
	if mode := req.String("sslmode"); mode != "" {
		env = append(env, "PGSSLMODE="+mode)
	}
	if ca := req.String("sslrootcert"); ca != "" {
		// The same keyword dsn() writes into the in-process driver's
		// connection string, carried the only way a subprocess reads it —
		// pg_dump has no connection-string argument for a single value like
		// this one, only PG* environment variables and its own -h/-p/-U/-d.
		env = append(env, "PGSSLROOTCERT="+ca)
	}
	if home := os.Getenv("HOME"); home != "" {
		// pg_dump reads certificates and CRLs from under it for the verify-*
		// modes, so an sslmode the operator configured keeps working.
		env = append(env, "HOME="+home)
	}
	return env
}

// classifyDump turns pg_dump's exit into something an operator can act on —
// the same job classify does for the driver, for the failures that only
// happen out here.
func classifyDump(err error, stderr string, req plugin.Request) *view.Error {
	// **Report the line that explains it, not the last one.** pg_dump ends a
	// failure with `detail: The command was: COPY ...`, so the last line is
	// reliably the least informative — the replica-conflict path first
	// surfaced as a COPY statement with no hint of why it stopped. Each branch
	// below names what it matched on and gets that line back.
	msg := func(needles ...string) string {
		if line := lineMatching(stderr, needles...); line != "" {
			return line
		}
		if line := lastLine(stderr); line != "" {
			return line
		}
		return err.Error()
	}

	switch {
	// **The failure that only happens on a replica**, and the one nobody
	// diagnoses from the message. A dump on a hot standby holds a snapshot for
	// as long as it runs; when the primary vacuums away row versions that
	// snapshot still needs, replay would have to overwrite them, and the
	// standby resolves the conflict by cancelling the reader. So a dump that
	// works on a small database fails on a big one, intermittently, with a
	// message about "recovery" that names neither the dump nor the setting
	// that fixes it.
	case strings.Contains(stderr, "conflict with recovery"),
		strings.Contains(stderr, "canceling statement due to conflict"):
		return view.Errorf("pg.dump.replicaconflict",
			"the replica cancelled the dump to catch up with its primary: %s",
			msg("conflict with recovery", "canceling statement")).
			WithHint("a dump holds one snapshot for its whole run, and the standby killed it " +
				"rather than fall further behind. Set hot_standby_feedback = on so the primary " +
				"keeps what this snapshot needs, or raise max_standby_streaming_delay — or dump " +
				"from the primary, where nothing can cancel it")
	case strings.Contains(stderr, "pg_export_snapshot"),
		strings.Contains(stderr, "synchronized snapshot"):
		// Never answered by dropping to --no-synchronized-snapshots, which is
		// the flag that would make this "work": it turns one parallel dump into
		// N unrelated reads at different times, and produces a file that
		// restores cleanly into a state the database was never in.
		return view.Errorf("pg.dump.nosnapshot",
			"this server cannot share one snapshot across parallel workers: %s",
			msg("pg_export_snapshot", "synchronized snapshot")).
			WithHint("run it serially with --jobs 1, which uses a single transaction. rta will " +
				"not pass --no-synchronized-snapshots to make --jobs work here: that drops the " +
				"guarantee that every table came from the same instant, and a dump without it " +
				"restores without complaint into a state that never existed")
	case strings.Contains(stderr, "server version") && strings.Contains(stderr, "aborting"):
		// The one failure nobody guesses from the message, because it reads
		// like a server problem and is a client one.
		return view.Errorf("pg.dump.version", "%s", msg("server version")).
			WithHint("pg_dump refuses a server newer than itself — install a client at least " +
				"as new as the server `rta pg status` reports")
	case strings.Contains(stderr, "no password supplied"),
		strings.Contains(stderr, "password authentication failed"):
		return view.Errorf("pg.auth.failed", "%s", msg("password")).
			WithHint("set $" + plugin.LocalEnvVar("pg.dump", "password") +
				" — pg_dump is run with --no-password so it fails here instead of " +
				"waiting at a prompt nothing can answer")
	case strings.Contains(stderr, "permission denied"):
		return view.Errorf("pg.denied", "%s", msg("permission denied")).
			WithHint("dumping every table needs a role that can read every table — this is " +
				"the database refusing, not rta")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return view.Errorf("pg.dump.cancelled", "the dump was interrupted").
			WithHint("the partial file has been removed")
	}
	return view.Errorf("pg.dump.failed", "%s", msg("error:")).
		WithHint("`" + filepath.Base(dumpTools[0]) + "` reported this; rta passed it through unchanged")
}

// lineMatching returns the first stderr line containing any of the needles.
//
// pg_dump reports a failure across several lines — an "error:" line, a
// "detail:" line carrying the server's own message, and a "detail: The
// command was:" line last. The useful one is whichever mentioned the thing
// that was matched on, which is never reliably the first or the last.
func lineMatching(stderr string, needles ...string) string {
	for _, line := range strings.Split(stderr, "\n") {
		for _, needle := range needles {
			if strings.Contains(line, needle) {
				return strings.TrimSpace(line)
			}
		}
	}
	return ""
}

func lookupDumpTool() (string, error) {
	var err error
	for _, name := range dumpTools {
		p, e := exec.LookPath(name)
		if e == nil {
			return p, nil
		}
		err = e
	}
	return "", err
}

// contentsOf says what is in the file, in the words the flags used.
func contentsOf(req plugin.Request) string {
	what := "schema and rows"
	switch req.String("include") {
	case "schema":
		what = "schema only, no rows"
	case "data":
		what = "rows only, no schema"
	}
	what += ", " + req.String("format") + " format"
	if n := req.Int("jobs"); n > 1 {
		what += fmt.Sprintf(", %d workers", n)
	}
	return what
}

// restoreCommand names the other half. A backup capability that does not say
// how to restore is the shape of every backup that turned out not to be one.
//
// **--jobs carries over to the restore**, which is the half people forget: a
// dump written by eight workers restores serially unless you ask, and the
// restore is usually the slower direction because it rebuilds every index.
func restoreCommand(req plugin.Request, path string) string {
	where := fmt.Sprintf("--host=%s --port=%d --username=%s",
		req.String("host"), req.Int("port"), req.String("user"))
	switch req.String("format") {
	case "custom", "directory":
		jobs := ""
		if n := req.Int("jobs"); n > 1 {
			jobs = fmt.Sprintf(" --jobs=%d", n)
		}
		return fmt.Sprintf("pg_restore %s%s --dbname=%s %s",
			where, jobs, req.String("database"), path)
	}
	return fmt.Sprintf("psql %s --dbname=%s --file=%s", where, req.String("database"), path)
}

func backupSuffix(f string) string {
	switch f {
	case "custom":
		return ".dump"
	case "directory":
		// A directory, so no extension — but a name that says what it is,
		// since `--out ./app` giving back a directory surprises people.
		return "-backup"
	}
	return ".sql"
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// expandHome resolves a leading ~ the way every other consumer of a Path
// input in this codebase does. Mirrors builtin/kv, builtin/keys and
// plugins/s3's own copies rather than centralizing a shared helper: an
// external plugin cannot reach internal/pathguard, and the rule is ten lines.
func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return filepath.Abs(p)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if p == "~" {
		return home, nil
	}
	return filepath.Join(home, p[2:]), nil
}
