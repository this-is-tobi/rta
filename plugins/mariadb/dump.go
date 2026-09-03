package main

import (
	"context"
	"errors"
	"fmt"
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

// The whole database, for a person, as a file — pg.dump's posture, with the
// differences MariaDB itself imposes rather than ones this plugin invented.
//
// **It shells out to mariadb-dump rather than reimplementing it**, pg.dump's
// most important decision repeated: a restorable dump has to get character
// sets, generated columns, triggers, routines and quoting right, and a file
// that will not restore is worse than no capability at all. builtin/kv sets
// the precedent for depending on a tool that is simply present or simply not.
//
// **Three flags the mysql plugin passes are deliberately absent here**, and
// they are the reason this is not a thin alias over that one. MariaDB's
// client is a fork that diverged: --set-gtid-purged and --no-tablespaces are
// MySQL 8 spellings its client does not know (GTID is opt-in through --gtid
// and off by default, and the tablespace DDL that needs PROCESS is a MySQL 8
// behaviour MariaDB never grew), and TLS is asked for with the --ssl family
// rather than --ssl-mode. Passing a flag the client does not have is not a
// degraded dump, it is an immediate "unknown option" — which classifyDump
// names, because a client answering to the other fork's name is the failure
// somebody will actually hit.
//
// **There is one format and no --jobs.** mariadb-dump writes SQL and nothing
// else — pg's custom/directory formats and parallel workers have no
// equivalent here, so the flags do not exist rather than existing and being
// refused.
//
// **The consistency guarantee is real for transactional tables and absent
// for the rest**, and the receipt says which is which. --single-transaction
// opens one REPEATABLE READ snapshot, so every InnoDB table is read from a
// single instant — but Aria and MyISAM have no transactions to join and are
// read live, mid-write if a write is happening. Aria is the one that catches
// people here and not in the mysql twin: it is MariaDB's own crash-safe
// engine, it is what system tables use, and being crash-safe is not being
// transactional. Claiming "one snapshot" over a database holding either
// would be the working-but-wrong receipt this family refuses to print, so
// the source is asked first and the caveat is counted, not assumed.
//
// **And the Galera question the mysql twin cannot ask.** A node that has
// lost quorum still accepts connections and still answers queries — from its
// own side of a partition. A dump taken there is a backup of that side, and
// the file does not know it, so the receipt says it while somebody is still
// looking.
//
// It refuses MCP outright for pg.dump's reason: a whole database has no
// blast radius a grant could name. NeedsGrant stays unset — keys.backup's
// rule, that a grant which can never be exercised is an entry in
// `grant list` meaning nothing.

// dumpTools are tried in order: MariaDB 10.5 introduced the mariadb-* names
// and 11.0 made them primary, but every distribution still ships mysqldump
// as a symlink to the same binary, and plenty of machines have only that.
// The new name first so a host carrying both forks' clients gets MariaDB's.
var dumpTools = []string{"mariadb-dump", "mysqldump"}

// humanOnly is this plugin's copy of the gate builtin/keys opens with and
// pg.dump repeats. It comes first, before a connection is opened, so an
// agent's call never spends the operator's password on a question that was
// always going to be answered no. The hint is the caller's, because the dump
// and the restore refuse for mirrored reasons.
func humanOnly(req plugin.Request, id, hint string) *view.Error {
	if req.Surface() != plugin.SurfaceMCP {
		return nil
	}
	return view.Refusef("mariadb.human", "%s can only be run by a person at a terminal", id).
		WithHint(hint)
}

func dumpCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "mariadb.dump",
		Summary: "Back up one database to a SQL file, for a person at a terminal",
		Safety:  plugin.Write,
		// Running it twice at the same --out refuses rather than overwriting.
		Idempotent: false,
		Description: "The whole database as a SQL file you can restore. **Refuses MCP outright " +
			"rather than asking for a grant** — pg.dump's line: a full dump's one authorized use " +
			"is everything, and an agent that needs rows asks for mariadb.query, which is bounded " +
			"per call.\n\n" +
			"Runs `mariadb-dump` rather than reimplementing it: a restorable dump has to get " +
			"character sets, triggers, routines and quoting right, and a file that will not " +
			"restore is worse than no capability at all. Routines, events and triggers are " +
			"included — the client omits routines and events by default, which is how dumps " +
			"quietly stop round-tripping. The password reaches the child through its " +
			"environment, never argv; option files are ignored (--no-defaults), so an ambient " +
			"~/.my.cnf credential is never silently spent.\n\n" +
			"Consistent for what can be: --single-transaction reads every InnoDB table from one " +
			"snapshot, and the receipt counts the non-transactional tables — Aria and MyISAM — " +
			"read live outside it rather than claiming a guarantee they cannot have. On a Galera " +
			"node the receipt also says whether the node held quorum: one that has lost it still " +
			"answers queries, from its own side of the partition, and nothing about the resulting " +
			"file would say so afterwards.\n\n" +
			"MariaDB's client is a fork, not an alias, so the flags differ from the mysql " +
			"plugin's by necessity: TLS through the --ssl family rather than --ssl-mode, and no " +
			"--set-gtid-purged or --no-tablespaces, which this client does not have — GTID stays " +
			"out by default here, which is what that spelling has to ask for over there. A flag " +
			"the installed client does not know is refused by name, since the likeliest cause is " +
			"MySQL's own tools answering to it.\n\n" +
			"Created with O_EXCL at 0600, never over an existing file; a failed run takes its " +
			"half-written file with it. The receipt names the restore command.",
		Run: runDump,
	},
		// Local for the usual reason — a destination is a destination — and
		// so a caller can never choose which file on the host is written.
		// Belt and braces beside the MCP refusal; the two protect against
		// different mistakes.
		plugin.Field{Name: "out", Type: plugin.Path, Local: true,
			Help: "file to write; refused if it already exists"},
		plugin.Field{Name: "include", Type: plugin.String, Default: "all",
			Options: []string{"all", "schema", "data"},
			Help:    "what to put in the file"})
}

func runDump(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "mariadb.dump",
		"a whole-database dump has no blast radius a grant could name — its one authorized "+
			"use is everything. Ask for the rows you need with mariadb.query, which is bounded "+
			"per call"); verr != nil {
		return nil, verr
	}

	database := req.String("database")
	if database == "" {
		return nil, view.Errorf("mariadb.dump.nodatabase", "say which database to dump").
			WithHint("--database <name> — `rta mariadb database list` shows what is there")
	}
	out := strings.TrimSpace(req.String("out"))
	if out == "" {
		return nil, view.Errorf("mariadb.dump.nooutput", "say where the dump should be written").
			WithHint("--out ./" + database + ".sql — a whole database is a file, not something " +
				"to read in a terminal")
	}
	path, err := expandHome(out)
	if err != nil {
		return nil, view.Errorf("mariadb.dump.path", "resolving --out: %v", err)
	}

	tool, err := lookupTool(dumpTools)
	if err != nil {
		return nil, view.Errorf("mariadb.dump.missing", "no %s on $PATH",
			strings.Join(dumpTools, " or ")).
			WithHint("rta does not reimplement it: a dump has to get character sets, triggers, " +
				"routines and quoting right, and one that will not restore is worse than none. " +
				"Install the MariaDB client tools — `brew install mariadb` or " +
				"`apt install mariadb-client`")
	}
	// A friendly early refusal, before anything opens a connection to say the
	// same thing more slowly. Not the guarantee — O_EXCL in writeDump is, and
	// that still catches the race this stat cannot.
	if _, err := os.Stat(path); err == nil {
		return nil, alreadyThere(path)
	}

	args := dumpArgs(req)
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would run %s %s\nand write %s",
			filepath.Base(tool), strings.Join(args, " "), path)}, nil
	}

	// **Ask the server what it is before dumping it** — pg.dump's
	// describeSource discipline. What comes back decides what the receipt may
	// honestly claim: the version, whether this is a read-only server, and
	// how many tables sit outside the snapshot guarantee.
	src, verr := describeSource(ctx, req, database)
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
		{Key: "consistency", Value: src.consistency()},
		// Named on the answer rather than left in the docs. The file is every
		// row in the database in the clear, and the moment to say so is while
		// somebody is looking at where it landed.
		{Key: "at rest", Value: "unencrypted, mode 0600 — `rta kv` or `age` if it is going anywhere"},
		{Key: "restore with", Value: restoreCommand(req, path)},
	}}, nil
}

// source is what the server said it is when asked, just before the dump.
type source struct {
	version  string
	readOnly bool
	// galeraStatus is wsrep_cluster_status when this server is a Galera node
	// and empty when it is not, which is the fact this plugin exists to know
	// and the mysql twin cannot. A node that has lost quorum still accepts
	// connections and still answers queries — it just answers them from a
	// partition — so a dump taken there is a backup of one side of a split
	// brain, and nothing about the file says so afterwards.
	galeraStatus string
	// liveTables is how many BASE TABLEs in this database are not
	// transactional — MyISAM and friends — and therefore read live, outside
	// the snapshot --single-transaction opens.
	liveTables int
}

func (s source) describe() string {
	where := s.version
	if s.readOnly {
		// A read-only server is usually a replica, and a replica's dump is as
		// current as its replication lag — said on the receipt because it
		// changes what the backup means.
		where += " — read-only, likely a replica: the dump is as current as its replication lag"
	}
	switch {
	case s.galeraStatus == "":
		// Standalone, or a build without Galera. Nothing to say.
	case strings.EqualFold(s.galeraStatus, "Primary"):
		where += " — a Galera node holding quorum"
	default:
		// The one that changes what the backup means, said while somebody is
		// looking at the file they just made rather than left for the day
		// they restore it.
		where += fmt.Sprintf(" — **a Galera node whose cluster status is %s, not Primary**: "+
			"this node has lost quorum and is serving its own side of a partition, so this "+
			"dump is a backup of that side. `rta mariadb cluster` has the whole picture",
			s.galeraStatus)
	}
	return where
}

func (s source) consistency() string {
	base := "one REPEATABLE READ snapshot (--single-transaction) for transactional tables"
	if s.liveTables > 0 {
		return fmt.Sprintf("%s — but %d non-transactional table(s) were read live, outside it",
			base, s.liveTables)
	}
	return base
}

func describeSource(ctx context.Context, req plugin.Request, database string) (source, *view.Error) {
	db, verr := connect(ctx, req)
	if verr != nil {
		return source{}, verr
	}
	defer func() { _ = db.Close() }()

	var s source
	if err := db.QueryRowContext(ctx, "select version()").Scan(&s.version); err != nil {
		return source{}, classify(err, req)
	}
	var ro int
	if err := db.QueryRowContext(ctx, "select @@read_only").Scan(&ro); err != nil {
		return source{}, classify(err, req)
	}
	s.readOnly = ro != 0
	// Aria as well as InnoDB: it is MariaDB's own crash-safe engine and the
	// default for system tables, but it is not transactional, so an Aria
	// table is read live and outside the snapshot exactly as MyISAM is. The
	// mysql twin's list is shorter because MySQL has no Aria — the kind of
	// divergence that makes these two plugins rather than one.
	if err := db.QueryRowContext(ctx, `
		select count(*) from information_schema.tables
		where table_schema = ? and table_type = 'BASE TABLE'
		  and engine is not null and engine not in ('InnoDB')`,
		database).Scan(&s.liveTables); err != nil {
		return source{}, classify(err, req)
	}
	// Galera state, best effort and deliberately not fatal: a server built
	// without wsrep answers this with an empty provider (or nothing at all),
	// and a standalone MariaDB is not a broken cluster — the same reading
	// cluster.go's own runCluster makes.
	var name, status string
	if err := db.QueryRowContext(ctx,
		`show global status like 'wsrep_cluster_status'`).Scan(&name, &status); err == nil {
		s.galeraStatus = status
	}
	return s, nil
}

// dumpArgs builds mariadb-dump's argv.
//
// **Never a shell string, and never the password.** argv is world-readable
// through `ps`, so the credential goes to the child through MYSQL_PWD and
// nowhere else. --no-defaults comes first because that is where the client
// requires it, and it is the argv half of the ambient-credential rule: the
// operator's ~/.my.cnf is not silently read, the same closure childEnv's
// PGPASSFILE line makes for pg.
func dumpArgs(req plugin.Request) []string {
	args := []string{
		"--no-defaults",
		"--host=" + req.String("host"),
		"--port=" + strconv.Itoa(req.Int("port")),
		"--user=" + req.String("user"),
		// One snapshot for everything transactional; describeSource counts
		// what falls outside it for the receipt.
		"--single-transaction",
		// No --no-tablespaces and no --set-gtid-purged here, unlike the mysql
		// plugin: both are MySQL 8 spellings this client does not have, and a
		// flag it does not have is an immediate "unknown option" rather than
		// a lesser dump. MariaDB needs neither anyway — GTID state is opt-in
		// through --gtid and stays out by default, which is the portable-seed
		// behaviour --set-gtid-purged=OFF has to ask for over there.
	}
	args = append(args, tlsArgs(req)...)
	switch req.String("include") {
	case "schema":
		args = append(args, "--no-data", "--routines", "--events", "--triggers")
	case "data":
		// Rows only: the schema half of triggers/routines/events was asked to
		// be left out, so they all are.
		args = append(args, "--no-create-info", "--skip-triggers")
	default:
		// Routines and events are opt-in flags upstream, and leaving them out
		// is how a dump quietly stops round-tripping: the schema restores,
		// the stored procedures the application calls do not exist.
		args = append(args, "--routines", "--events", "--triggers")
	}
	return append(args, req.String("database"))
}

// tlsArgs maps the plugin's tls input — go-sql-driver's vocabulary, shared
// with every in-process capability here — onto the MariaDB client's own
// --ssl family. **Not --ssl-mode**, which is the MySQL 8 spelling: the
// --ssl/--ssl-verify-server-cert pair is what MariaDB's client has carried
// across every version this is likely to meet, and choosing the spelling the
// installed fork does not know turns a working dump into "unknown option".
//
// The mapping is by *meaning*, matching the mysql plugin's: "true" both
// encrypts and verifies the server is who it claims (the driver's own
// behaviour for true), "skip-verify" encrypts without verifying,
// "preferred" is the client's default and passes nothing.
func tlsArgs(req plugin.Request) []string {
	switch req.String("tls") {
	case "false":
		return []string{"--skip-ssl"}
	case "true":
		return []string{"--ssl", "--ssl-verify-server-cert"}
	case "skip-verify":
		return []string{"--ssl"}
	}
	return nil
}

// writeDump creates the destination, runs the tool with its stdout handed
// the destination descriptor directly — no pipe, no copy through this
// process — and reports how much landed, removing whatever it made if the
// run fails: a partial dump left on disk is the one that gets restored six
// months later.
func writeDump(ctx context.Context, tool string, args []string,
	req plugin.Request, path string) (int64, *view.Error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return 0, alreadyThere(path)
	}
	if err != nil {
		return 0, view.Errorf("mariadb.dump.create", "creating %s: %v", path, err)
	}

	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Env = childEnv(req)

	runErr := cmd.Run()
	closeErr := f.Close()
	switch {
	case runErr != nil:
		_ = os.Remove(path)
		return 0, classifyDump(runErr, stderr.String(), req)
	case closeErr != nil:
		_ = os.Remove(path)
		return 0, view.Errorf("mariadb.dump.write", "finishing %s: %v", path, closeErr)
	}

	var size int64
	if info, err := os.Stat(path); err == nil {
		size = info.Size()
	}
	return size, nil
}

// childEnv is exactly what the child needs and nothing else — an unrelated
// token in the operator's shell is not something mariadb-dump could ever have
// printed.
func childEnv(req plugin.Request) []string {
	// LC_ALL=C because rta reads the child's stderr to classify it, and a
	// classifier that works in one locale and silently degrades to
	// "unrecognised failure" in another is worse than one that never worked.
	env := []string{"PATH=" + os.Getenv("PATH"), "LC_ALL=C"}
	if pw := req.String("password"); pw != "" {
		// The documented-as-insecure warning on MYSQL_PWD is about multi-user
		// machines of an earlier era reading /proc environments; on every
		// platform this runs on, a process's environment is readable only by
		// its own uid — while argv is readable by everyone through `ps`.
		// Between the two channels the child actually offers, this is the
		// closed one.
		env = append(env, "MYSQL_PWD="+pw)
	}
	return env
}

// classifyDump turns the child's exit into something an operator can act on
// — the same job classify does for the driver, for the failures that only
// happen out here.
func classifyDump(err error, stderr string, req plugin.Request) *view.Error {
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
	case strings.Contains(stderr, "Access denied") && strings.Contains(stderr, "PROCESS"):
		return view.Errorf("mariadb.denied", "%s", msg("PROCESS")).
			WithHint("this is the server refusing a privilege, not rta — check SHOW GRANTS for " +
				"the user this dump connects as")
	case strings.Contains(stderr, "Access denied"):
		return view.Errorf("mariadb.auth.failed", "%s", msg("Access denied")).
			WithHint("set $" + plugin.LocalEnvVar("mariadb.dump", "password") + ", or check --user")
	case strings.Contains(stderr, "Unknown database"):
		return view.Errorf("mariadb.database.notfound", "%s", msg("Unknown database")).
			WithHint("`rta mariadb database list` shows what is there")
	case strings.Contains(stderr, "Unknown MySQL server host"):
		return view.Errorf("mariadb.host.unknown", "%s", msg("Unknown MySQL server host")).
			WithHint("`rta net dns " + req.String("host") + "` shows what DNS returns")
	case strings.Contains(stderr, "Can't connect"):
		return view.Errorf("mariadb.conn.refused", "%s", msg("Can't connect")).
			WithHint("is the server up, and is --host/--port right?")
	case strings.Contains(stderr, "unknown variable") || strings.Contains(stderr, "unknown option"):
		// The version-skew failure, and here it is the likelier one: this
		// plugin passes MariaDB's --ssl family, so a *MySQL* client wearing
		// the mysqldump name — the shape on a host with only MySQL's tools
		// installed — refuses on the first of them.
		return view.Errorf("mariadb.dump.toolskew", "%s", msg("unknown")).
			WithHint("the installed client does not speak this flag — `mysqldump --version` " +
				"says which fork it really is. This plugin drives MariaDB's client; for a " +
				"MySQL server, the mysql plugin drives mysqldump with the flags that client " +
				"actually has")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return view.Errorf("mariadb.dump.cancelled", "the dump was interrupted").
			WithHint("the partial file has been removed")
	}
	return view.Errorf("mariadb.dump.failed", "%s", msg("error:", "Error:")).
		WithHint("`" + filepath.Base(dumpTools[0]) + "` reported this; rta passed it through unchanged")
}

// restoreCommand names the other half. A backup capability that does not say
// how to restore is the shape of every backup that turned out not to be one.
func restoreCommand(req plugin.Request, path string) string {
	return fmt.Sprintf("rta mariadb restore %s --host=%s --port=%d --user=%s --database=%s",
		path, req.String("host"), req.Int("port"), req.String("user"), req.String("database"))
}

func alreadyThere(path string) *view.Error {
	return view.Errorf("mariadb.dump.exists", "%s already exists", path).
		WithHint("a dump is never written over an existing file — name a new one, or move " +
			"that one aside")
}

func lookupTool(names []string) (string, error) {
	var err error
	for _, name := range names {
		p, e := exec.LookPath(name)
		if e == nil {
			return p, nil
		}
		err = e
	}
	return "", err
}

func contentsOf(req plugin.Request) string {
	switch req.String("include") {
	case "schema":
		return "schema only — tables, routines, events and triggers, no rows"
	case "data":
		return "rows only, no schema"
	}
	return "schema and rows, with routines, events and triggers"
}

// lineMatching returns the first stderr line containing any of the needles —
// the child reports failures across several lines, and the useful one is
// whichever mentioned the thing that was matched on.
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

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// expandHome resolves a leading ~ the way every other consumer of a Path
// input in this codebase does. Mirrors builtin/kv, builtin/keys and
// plugins/pg's own copies rather than centralizing a shared helper: an
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
