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

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The other half of mariadb.dump — the file back into a database.
//
// **It refuses MCP for the dump's reason run in reverse**, and it is
// Destructive besides: whatever the file says is written into a live
// database, and mariadb-dump's default output drops and recreates every table
// it carries on the way in (--add-drop-table is part of --opt). The --yes
// gate is what a person should type through before that happens.
//
// **The guarantee is honest and weaker than pg's, because MySQL's is.**
// psql can replay a plain dump inside one transaction; MySQL cannot — DDL
// commits implicitly, so there is no transaction to roll a failed restore
// back with. What remains is stop-at-first-error, and the load-bearing
// choice here is an *absence*: --force is never passed, because it is the
// flag that counts errors quietly to the end and calls the survivor a
// restore. The receipt says what a failure would have meant.
//
// **A non-empty target is refused**, the dump's O_EXCL pointing the other
// way — and with no --clean to offer: whether this dump drops objects first
// was decided when mariadb-dump wrote it, exactly like a plain pg dump, so the
// path through is a fresh database and the refusal says how to make one.
// rta does not create it: a typo'd name becoming a new database is worse
// than this refusal.

// restoreTools are tried in order, for dumpTools' reason: MariaDB's own
// client name first, the legacy symlink every distribution still ships
// second, so a host carrying both forks' clients gets MariaDB's.
var restoreTools = []string{"mariadb", "mysql"}

func restoreCapability() plugin.Capability {
	return cap(plugin.Capability{
		ID:      "mariadb.restore",
		Summary: "Restore a mariadb.dump file into a database, for a person at a terminal",
		// Destructive, because that is what it is: the dump's SQL drops and
		// recreates the tables it carries. The class buys the --yes gate.
		Safety:     plugin.Destructive,
		Idempotent: false,
		Description: "The other half of mariadb.dump — the file back into a database. **Refuses MCP " +
			"outright** for the dump's reason run in reverse: the dump refuses because everything " +
			"would leave, and a restore is everything arriving, written into a live database — " +
			"with mariadb-dump's own DROP TABLE statements running first. Neither direction has a " +
			"blast radius a grant could name, so both belong to the person at the keyboard.\n\n" +
			"**A database already holding tables is refused.** Whether a dump drops objects " +
			"first was decided when mariadb-dump wrote it, so there is no --clean to offer — restore " +
			"into a fresh database, which stays one CREATE DATABASE away. rta does not create it: " +
			"a typo'd name becoming a new database is worse than the refusal. A read-only server " +
			"— a replica, usually — is refused before anything runs; restore on the primary, " +
			"which is the only path that keeps the two the same database.\n\n" +
			"Stops at the first error, which is the strongest guarantee MySQL allows: DDL commits " +
			"implicitly, so a failed restore cannot roll back and the receipt says so. --force — " +
			"the flag that counts errors quietly and calls the survivor a restore — is never " +
			"passed. LOAD DATA LOCAL is disabled, so a dump file cannot direct the client to " +
			"read this machine's own files into the server.",
		Run: runRestore,
	},
		plugin.Field{Name: "file", Type: plugin.Path, Local: true, Positional: true,
			Required: true,
			Help:     "the dump to restore — what mariadb.dump wrote"})
}

func runRestore(ctx context.Context, req plugin.Request) (view.View, error) {
	if verr := humanOnly(req, "mariadb.restore",
		"a restore writes a file's whole contents into a live database, dropping tables on "+
			"the way in — a blast radius no grant could name, in the direction that overwrites. "+
			"The dump this file came from was made by a person at a terminal, and it goes back "+
			"the same way"); verr != nil {
		return nil, verr
	}

	database := req.String("database")
	if database == "" {
		return nil, view.Errorf("mariadb.restore.nodatabase", "say which database to restore into").
			WithHint("--database <name> — `rta mariadb database list` shows what is there, and " +
				"CREATE DATABASE makes a fresh one")
	}
	path, err := expandHome(strings.TrimSpace(req.String("file")))
	if err != nil {
		return nil, view.Errorf("mariadb.restore.path", "resolving the dump path: %v", err)
	}
	if verr := checkDumpFile(path); verr != nil {
		return nil, verr
	}
	tool, err := lookupTool(restoreTools)
	if err != nil {
		return nil, view.Errorf("mariadb.restore.missing", "no %s on $PATH",
			strings.Join(restoreTools, " or ")).
			WithHint("rta does not reimplement it, for mariadb.dump's reason run in reverse. " +
				"Install the MariaDB client tools — `brew install mariadb` or " +
				"`apt install mariadb-client`")
	}

	args := restoreArgs(req)
	if req.DryRun {
		return view.Text{Body: fmt.Sprintf("would run %s %s\nfeeding it %s, restoring into %s on %s:%d",
			filepath.Base(tool), strings.Join(args, " "), path,
			database, req.String("host"), req.Int("port"))}, nil
	}

	// Ask the server what it is before writing into it — the dump's
	// describeSource discipline, with the two questions only a restore has to
	// ask: a read-only server cannot be written, and a database already
	// holding tables is refused.
	if verr := checkTarget(ctx, req, database); verr != nil {
		return nil, verr
	}

	started := time.Now()
	if verr := runRestoreTool(ctx, tool, args, req, path); verr != nil {
		return nil, verr
	}

	return view.KeyValue{Pairs: []view.Pair{
		{Key: "restored", Value: path},
		{Key: "into", Value: fmt.Sprintf("%s on %s:%d",
			database, req.String("host"), req.Int("port"))},
		{Key: "took", Value: time.Since(started).Round(time.Millisecond).String()},
		// pg's restore can promise a rollback; this one cannot, and saying so
		// here is what keeps the difference from being discovered during an
		// incident.
		{Key: "guarantee", Value: "stopped at the first error — MySQL DDL commits implicitly, " +
			"so a failure leaves a partial restore, and a fresh database is the clean way back"},
	}}, nil
}

// checkDumpFile refuses the two files that would waste a destructive call:
// one that is not there, and one that is empty — an empty file restores as
// nothing and reports success, and only rta can say "the dump did not
// finish"; the server just sees no statements.
func checkDumpFile(path string) *view.Error {
	info, err := os.Stat(path)
	if err != nil {
		return view.Errorf("mariadb.restore.nofile", "no dump at %s", path).
			WithHint("`rta mariadb dump --out <path>` writes one; this restores what that wrote")
	}
	if info.IsDir() {
		return view.Errorf("mariadb.restore.notafile", "%s is a directory", path).
			WithHint("a mariadb.dump file is a single SQL file")
	}
	if info.Size() == 0 {
		return view.Errorf("mariadb.restore.empty", "%s is empty", path).
			WithHint("an empty file restores as nothing and reports success — if this was a " +
				"dump, it did not finish")
	}
	return nil
}

// restoreArgs builds the mysql client's argv — never a shell string, never
// the password (childEnv's MYSQL_PWD), option files ignored for the dump's
// ambient-credential reason.
func restoreArgs(req plugin.Request) []string {
	args := []string{
		"--no-defaults",
		"--host=" + req.String("host"),
		"--port=" + strconv.Itoa(req.Int("port")),
		"--user=" + req.String("user"),
		// A dump file can contain LOAD DATA LOCAL INFILE, which directs the
		// *client* to read a file off this machine and hand it to the server.
		// The file being restored is the operator's own artifact, but closing
		// the primitive costs nothing and a hostile dump is not unthinkable.
		"--local-infile=0",
		// Non-interactive output; and note what is absent: --force, the flag
		// that would count errors quietly to the end and call the survivor a
		// restore. Without it the client stops at the first error, which is
		// the whole guarantee this direction has.
		"--batch",
	}
	args = append(args, tlsArgs(req)...)
	return append(args, req.String("database"))
}

// checkTarget asks the server what it is before anything writes into it, on
// one connection.
func checkTarget(ctx context.Context, req plugin.Request, database string) *view.Error {
	db, verr := connect(ctx, req)
	if verr != nil {
		// The driver has already classified an absent database (1049), but
		// its hint points at `mysql database list` — the right next step for
		// a typo in mysql.status and the wrong one here: half the time the
		// missing database is the fresh target somebody has not created yet.
		// Same fact, restore's advice.
		if verr.Code == "mariadb.database.notfound" {
			return view.Errorf("mariadb.restore.notarget", "%s", verr.Message).
				WithHint("rta does not create databases on its own — a typo'd name becoming a " +
					"new database is worse than this refusal. `CREATE DATABASE " + database +
					"` makes it, then restore again")
		}
		return verr
	}
	defer func() { _ = db.Close() }()

	var ro int
	if err := db.QueryRowContext(ctx, "select @@read_only").Scan(&ro); err != nil {
		return classify(err, req)
	}
	if ro != 0 {
		return view.Errorf("mariadb.restore.readonly",
			"%s:%d is read-only, and a read-only server cannot be written",
			req.String("host"), req.Int("port")).
			WithHint("usually a replica — restore on the primary, which the replica then " +
				"replays; that is the only path that keeps the two the same database")
	}

	var tables int
	if err := db.QueryRowContext(ctx, `
		select count(*) from information_schema.tables where table_schema = ?`,
		database).Scan(&tables); err != nil {
		return classify(err, req)
	}
	if tables > 0 {
		return view.Errorf("mariadb.restore.notempty",
			"%s already holds %d tables", database, tables).
			WithHint("restore into a fresh database — CREATE DATABASE is one command, and " +
				"whether this dump drops objects first was decided when mariadb-dump wrote it, " +
				"so rta will not guess on its behalf")
	}
	return nil
}

// runRestoreTool runs the child with the dump on its stdin — the descriptor
// handed over directly, so the bytes never pass through this process — and
// its stdout discarded: the client's output is for a terminal, and this
// handler's answer is the receipt.
func runRestoreTool(ctx context.Context, tool string, args []string,
	req plugin.Request, path string) *view.Error {
	f, err := os.Open(path)
	if err != nil {
		return view.Errorf("mariadb.restore.unreadable", "opening %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdin = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Env = childEnv(req)

	if err := cmd.Run(); err != nil {
		return classifyRestore(err, stderr.String(), req)
	}
	return nil
}

// classifyRestore turns the child's exit into something an operator can act
// on — classifyDump's job, for the failures that only happen in this
// direction.
func classifyRestore(err error, stderr string, req plugin.Request) *view.Error {
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
	case strings.Contains(stderr, "Unknown database"):
		return view.Errorf("mariadb.restore.notarget", "%s", msg("Unknown database")).
			WithHint("rta does not create databases on its own — CREATE DATABASE makes it, " +
				"then restore again")
	case strings.Contains(stderr, "Access denied"):
		return view.Errorf("mariadb.auth.failed", "%s", msg("Access denied")).
			WithHint("set $" + plugin.LocalEnvVar("mariadb.restore", "password") + ", or check --user")
	case strings.Contains(stderr, "read-only") || strings.Contains(stderr, "read only"):
		return view.Errorf("mariadb.restore.readonly", "%s", msg("read")).
			WithHint("the target became read-only after the pre-flight check — a promoted " +
				"replica, usually. Restore on the primary")
	case strings.Contains(stderr, "Can't connect"):
		return view.Errorf("mariadb.conn.refused", "%s", msg("Can't connect")).
			WithHint("is the server up, and is --host/--port right?")
	case strings.Contains(stderr, "at line"):
		// The client names the statement that failed and its line in the
		// file — the one detail worth surfacing verbatim, because it is where
		// the operator's investigation starts.
		return view.Errorf("mariadb.restore.failed", "%s", msg("at line")).
			WithHint("the restore stopped there; everything before it is applied — MySQL " +
				"cannot roll a restore back. A fresh database and a fresh run is the clean " +
				"way back")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return view.Errorf("mariadb.restore.cancelled", "the restore was interrupted").
			WithHint("everything up to the interruption is applied — a fresh database and a " +
				"fresh run is the clean way back")
	}
	return view.Errorf("mariadb.restore.failed", "%s", msg("ERROR")).
		WithHint("the tool reported this; rta passed it through unchanged")
}
