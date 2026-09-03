package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A whole database has no blast radius a grant could name, so it leaves the
// agent surface rather than asking for one — and the refusal is marked as a
// refusal, so the ledger files it under policy rather than "the work broke".
func TestDumpRefusesMCP(t *testing.T) {
	r := req(t, "mariadb.dump", map[string]any{
		"database": "app", "out": filepath.Join(t.TempDir(), "app.sql"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runDump(context.Background(), r)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.human" || !verr.Refusal {
		t.Fatalf("err = %v, want mariadb.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "mariadb.query") {
		t.Errorf("hint = %q, want it to name the bounded alternative", verr.Hint)
	}
}

func TestDumpRequiresADatabase(t *testing.T) {
	_, err := runDump(context.Background(), req(t, "mariadb.dump", map[string]any{
		"out": filepath.Join(t.TempDir(), "app.sql"),
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.dump.nodatabase" {
		t.Fatalf("err = %v, want mariadb.dump.nodatabase", err)
	}
}

func TestDumpRequiresAnOutput(t *testing.T) {
	_, err := runDump(context.Background(), req(t, "mariadb.dump", map[string]any{"database": "app"}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.dump.nooutput" {
		t.Fatalf("err = %v, want mariadb.dump.nooutput", err)
	}
}

// **The argv table.** Each row is a decision with a reason in dumpArgs'
// comments; this pins the decisions so a refactor cannot quietly drop one.
func TestDumpArgsCarryTheDecidedFlags(t *testing.T) {
	args := strings.Join(dumpArgs(req(t, "mariadb.dump", map[string]any{
		"database": "app", "out": "x.sql",
	})), " ")
	for _, want := range []string{
		"--single-transaction",                 // the consistency the receipt claims
		"--routines", "--events", "--triggers", // round-tripping, not the upstream default
	} {
		if !strings.Contains(args, want) {
			t.Errorf("dumpArgs missing %q in %q", want, args)
		}
	}
	// **The absences that make this plugin not an alias of the mysql one.**
	// Both are MySQL 8 spellings MariaDB's client does not have, and a flag
	// it does not have is an immediate "unknown option" rather than a lesser
	// dump — so a well-meaning copy from the twin would break every dump
	// here, and this is what would catch it.
	for _, mysqlOnly := range []string{"--no-tablespaces", "--set-gtid-purged"} {
		if strings.Contains(args, mysqlOnly) {
			t.Errorf("dumpArgs passes %q, which MariaDB's client does not have: %q", mysqlOnly, args)
		}
	}
	if !strings.HasPrefix(args, "--no-defaults") {
		t.Errorf("--no-defaults is not first, where the client requires it: %q", args)
	}
	if strings.Contains(args, "password") {
		t.Errorf("a password reached argv, which `ps` shows to everyone: %q", args)
	}
	if !strings.HasSuffix(args, " app") {
		t.Errorf("the database does not come last: %q", args)
	}
}

func TestDumpArgsIncludeNarrowsWhatTheFileHolds(t *testing.T) {
	schema := strings.Join(dumpArgs(req(t, "mariadb.dump", map[string]any{
		"database": "app", "include": "schema",
	})), " ")
	if !strings.Contains(schema, "--no-data") || !strings.Contains(schema, "--routines") {
		t.Errorf("include=schema: %q, want --no-data with routines kept", schema)
	}
	data := strings.Join(dumpArgs(req(t, "mariadb.dump", map[string]any{
		"database": "app", "include": "data",
	})), " ")
	if !strings.Contains(data, "--no-create-info") || !strings.Contains(data, "--skip-triggers") {
		t.Errorf("include=data: %q, want --no-create-info --skip-triggers", data)
	}
	if strings.Contains(data, "--routines") {
		t.Errorf("include=data still dumps routines: %q", data)
	}
}

// The tls input speaks go-sql-driver's vocabulary everywhere in this plugin;
// the child speaks the --ssl family. **Not --ssl-mode**, which is the MySQL 8
// spelling — pinned here because it is the single likeliest thing to be
// copied across from the mysql twin, and it would fail on every MariaDB
// client rather than degrading.
func TestTLSModesMapByMeaning(t *testing.T) {
	for tls, want := range map[string][]string{
		"false":       {"--skip-ssl"},
		"true":        {"--ssl", "--ssl-verify-server-cert"},
		"skip-verify": {"--ssl"},
	} {
		args := dumpArgs(req(t, "mariadb.dump", map[string]any{
			"database": "app", "tls": tls,
		}))
		joined := strings.Join(args, " ")
		for _, w := range want {
			if !slices.Contains(args, w) {
				t.Errorf("tls=%s: missing %q in %q", tls, w, joined)
			}
		}
		if strings.Contains(joined, "--ssl-mode") {
			t.Errorf("tls=%s: passes MySQL's --ssl-mode, which this client does not have: %q",
				tls, joined)
		}
	}
	// skip-verify encrypts without verifying, so the verify flag must be the
	// difference between it and "true" rather than something both carry.
	skip := dumpArgs(req(t, "mariadb.dump", map[string]any{
		"database": "app", "tls": "skip-verify",
	}))
	if slices.Contains(skip, "--ssl-verify-server-cert") {
		t.Errorf("tls=skip-verify still verifies the server: %v", skip)
	}
	preferred := strings.Join(dumpArgs(req(t, "mariadb.dump", map[string]any{
		"database": "app", "tls": "preferred",
	})), " ")
	if strings.Contains(preferred, "ssl") {
		t.Errorf("tls=preferred passes a flag where the client default is the meaning: %q", preferred)
	}
}

// The password travels through the child's environment, never argv, and the
// environment holds nothing else of the operator's shell.
func TestChildEnvCarriesThePasswordAndNothingElse(t *testing.T) {
	t.Setenv("SOME_UNRELATED_TOKEN", "leaky")
	env := childEnv(req(t, "mariadb.dump", map[string]any{
		"database": "app", "password": "s3cret",
	}))
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "MYSQL_PWD=s3cret") {
		t.Errorf("no MYSQL_PWD in %q", joined)
	}
	if !strings.Contains(joined, "LC_ALL=C") {
		t.Error("stderr is classified by text, so the child's locale must be pinned")
	}
	if strings.Contains(joined, "SOME_UNRELATED_TOKEN") {
		t.Error("an unrelated shell variable reached the child")
	}
}

// needsDumpTool skips a test that cannot reach its subject without a client
// on $PATH — pg's needsPgDump pattern, same reasoning: the lookup comes
// before the check under test, and that order is right for the operator. It
// walks dumpTools rather than naming one binary, so a host carrying only the
// legacy mysqldump symlink (most of them) runs these rather than skipping.
func needsDumpTool(t *testing.T) {
	t.Helper()
	if _, err := lookupTool(dumpTools); err != nil {
		t.Skipf("none of %v is on $PATH, and this test's subject is only reachable past the lookup",
			dumpTools)
	}
}

func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	needsDumpTool(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(path, []byte("the backup that already existed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runDump(context.Background(), req(t, "mariadb.dump", map[string]any{
		"database": "app", "out": path, "host": "127.0.0.1", "port": 1,
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.dump.exists" {
		t.Fatalf("err = %v, want mariadb.dump.exists", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "the backup that already existed" {
		t.Errorf("the existing file was disturbed: %q, %v", body, err)
	}
}

// A run that fails takes its half-written file with it — nothing is
// listening on port 1, so the pre-flight connection refuses before any
// file exists, and if the order ever changed, the child would fail after
// creating one; either way nothing may remain.
func TestAFailedDumpLeavesNoFileBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.sql")
	_, err := runDump(context.Background(), req(t, "mariadb.dump", map[string]any{
		"database": "app", "out": path, "host": "127.0.0.1", "port": 1,
	}))
	if err == nil {
		t.Skip("a dump against a closed port unexpectedly succeeded")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a failed dump left its partial file on disk, where it reads as a good one")
	}
}

func TestDumpDryRunDescribesWithoutConnecting(t *testing.T) {
	needsDumpTool(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	c := capabilityByID(t, "mariadb.dump")
	dry := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{
		"database": "app", "out": path, "host": "127.0.0.1", "port": 1,
	}}), true, false)

	v, err := runDump(context.Background(), dry)
	if err != nil {
		t.Fatalf("dry run failed against a dead endpoint, so it must have connected: %v", err)
	}
	text, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %T", v)
	}
	tool, _ := lookupTool(dumpTools)
	if !strings.Contains(text.Body, filepath.Base(tool)) || !strings.Contains(text.Body, path) {
		t.Errorf("dry run = %q, want the command and the destination", text.Body)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("--dry-run created the output file")
	}
}

// classifyDump's branches, each a sentence somebody has stared at.
func TestClassifyDumpNamesTheFailure(t *testing.T) {
	r := req(t, "mariadb.dump", map[string]any{"database": "app"})
	boom := errors.New("exit status 2")
	for name, tc := range map[string]struct {
		stderr string
		code   string
	}{
		"auth":      {"mariadb-dump: Got error: 1045: Access denied for user 'app'@'%'", "mariadb.auth.failed"},
		"process":   {"mariadb-dump: Error: Access denied; you need (at least one of) the PROCESS privilege(s)", "mariadb.denied"},
		"no db":     {"mariadb-dump: Got error: 1049: Unknown database 'app'", "mariadb.database.notfound"},
		"refused":   {"mariadb-dump: Got error: 2002: Can't connect to server", "mariadb.conn.refused"},
		"bad host":  {"mariadb-dump: Got error: 2005: Unknown MySQL server host 'db.internal'", "mariadb.host.unknown"},
		"tool skew": {"mysqldump: unknown variable 'ssl-verify-server-cert'", "mariadb.dump.toolskew"},
		"atstraws":  {"mariadb-dump: something nobody anticipated", "mariadb.dump.failed"},
	} {
		t.Run(name, func(t *testing.T) {
			verr := classifyDump(boom, tc.stderr, r)
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q (stderr %q)", verr.Code, tc.code, tc.stderr)
			}
			if verr.Hint == "" {
				t.Error("no hint")
			}
		})
	}
}

func capabilityByID(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}

// **The Galera fact the mysql twin cannot state.** A node that has lost
// quorum still accepts connections and still answers queries — from its own
// side of a partition — so a dump taken there backs up that side, and the
// file says nothing about it afterwards. The receipt has to, while somebody
// is still looking at it.
func TestTheReceiptNamesALostQuorum(t *testing.T) {
	split := source{version: "11.4.2-MariaDB", galeraStatus: "non-Primary"}.describe()
	if !strings.Contains(split, "non-Primary") || !strings.Contains(split, "lost quorum") {
		t.Errorf("describe() = %q, want it to say the node is not in the primary component", split)
	}
	if !strings.Contains(split, "rta mariadb cluster") {
		t.Errorf("describe() = %q, want it to name where the whole picture is", split)
	}

	healthy := source{version: "11.4.2-MariaDB", galeraStatus: "Primary"}.describe()
	if strings.Contains(healthy, "lost quorum") {
		t.Errorf("describe() = %q, want no warning for a node holding quorum", healthy)
	}

	// A standalone MariaDB is not a broken cluster — the same reading
	// cluster.go makes of an absent wsrep provider.
	standalone := source{version: "11.4.2-MariaDB"}.describe()
	if strings.Contains(standalone, "Galera") {
		t.Errorf("describe() = %q, want nothing about Galera on a standalone server", standalone)
	}
}

// Aria is MariaDB's own crash-safe engine and the default for system
// tables, and crash-safe is not transactional: an Aria table is read live,
// outside --single-transaction's snapshot, exactly as MyISAM is. The
// receipt counts them rather than claiming a guarantee they cannot have.
func TestTheReceiptCountsTablesOutsideTheSnapshot(t *testing.T) {
	c := source{liveTables: 3}.consistency()
	if !strings.Contains(c, "3 non-transactional") {
		t.Errorf("consistency() = %q, want the count of tables read live", c)
	}
	clean := source{}.consistency()
	if strings.Contains(clean, "non-transactional") {
		t.Errorf("consistency() = %q, want no caveat when every table is transactional", clean)
	}
}
