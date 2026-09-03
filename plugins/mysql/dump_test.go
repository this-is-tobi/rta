package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A whole database has no blast radius a grant could name, so it leaves the
// agent surface rather than asking for one — and the refusal is marked as a
// refusal, so the ledger files it under policy rather than "the work broke".
func TestDumpRefusesMCP(t *testing.T) {
	r := req(t, "mysql.dump", map[string]any{
		"database": "app", "out": filepath.Join(t.TempDir(), "app.sql"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runDump(context.Background(), r)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mysql.human" || !verr.Refusal {
		t.Fatalf("err = %v, want mysql.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "mysql.query") {
		t.Errorf("hint = %q, want it to name the bounded alternative", verr.Hint)
	}
}

func TestDumpRequiresADatabase(t *testing.T) {
	_, err := runDump(context.Background(), req(t, "mysql.dump", map[string]any{
		"out": filepath.Join(t.TempDir(), "app.sql"),
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mysql.dump.nodatabase" {
		t.Fatalf("err = %v, want mysql.dump.nodatabase", err)
	}
}

func TestDumpRequiresAnOutput(t *testing.T) {
	_, err := runDump(context.Background(), req(t, "mysql.dump", map[string]any{"database": "app"}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mysql.dump.nooutput" {
		t.Fatalf("err = %v, want mysql.dump.nooutput", err)
	}
}

// **The argv table.** Each row is a decision with a reason in dumpArgs'
// comments; this pins the decisions so a refactor cannot quietly drop one.
func TestDumpArgsCarryTheDecidedFlags(t *testing.T) {
	args := strings.Join(dumpArgs(req(t, "mysql.dump", map[string]any{
		"database": "app", "out": "x.sql",
	})), " ")
	for _, want := range []string{
		"--single-transaction",                 // the consistency the receipt claims
		"--no-tablespaces",                     // the PROCESS-privilege wall, avoided
		"--set-gtid-purged=OFF",                // a seed, not replica state
		"--routines", "--events", "--triggers", // round-tripping, not the upstream default
	} {
		if !strings.Contains(args, want) {
			t.Errorf("dumpArgs missing %q in %q", want, args)
		}
	}
	if !strings.HasPrefix(args, "--no-defaults") {
		t.Errorf("--no-defaults is not first, where mysqldump requires it: %q", args)
	}
	if strings.Contains(args, "password") {
		t.Errorf("a password reached argv, which `ps` shows to everyone: %q", args)
	}
	if !strings.HasSuffix(args, " app") {
		t.Errorf("the database does not come last: %q", args)
	}
}

func TestDumpArgsIncludeNarrowsWhatTheFileHolds(t *testing.T) {
	schema := strings.Join(dumpArgs(req(t, "mysql.dump", map[string]any{
		"database": "app", "include": "schema",
	})), " ")
	if !strings.Contains(schema, "--no-data") || !strings.Contains(schema, "--routines") {
		t.Errorf("include=schema: %q, want --no-data with routines kept", schema)
	}
	data := strings.Join(dumpArgs(req(t, "mysql.dump", map[string]any{
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
// the child speaks --ssl-mode. The mapping is by meaning, and "preferred"
// passes nothing because it is the client's own default.
func TestTLSModesMapByMeaning(t *testing.T) {
	for tls, want := range map[string]string{
		"false":       "--ssl-mode=DISABLED",
		"true":        "--ssl-mode=VERIFY_IDENTITY",
		"skip-verify": "--ssl-mode=REQUIRED",
	} {
		args := strings.Join(dumpArgs(req(t, "mysql.dump", map[string]any{
			"database": "app", "tls": tls,
		})), " ")
		if !strings.Contains(args, want) {
			t.Errorf("tls=%s: missing %q in %q", tls, want, args)
		}
	}
	preferred := strings.Join(dumpArgs(req(t, "mysql.dump", map[string]any{
		"database": "app", "tls": "preferred",
	})), " ")
	if strings.Contains(preferred, "--ssl-mode") {
		t.Errorf("tls=preferred passes a flag where the client default is the meaning: %q", preferred)
	}
}

// The password travels through the child's environment, never argv, and the
// environment holds nothing else of the operator's shell.
func TestChildEnvCarriesThePasswordAndNothingElse(t *testing.T) {
	t.Setenv("SOME_UNRELATED_TOKEN", "leaky")
	env := childEnv(req(t, "mysql.dump", map[string]any{
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

// needsMysqldump skips a test that cannot reach its subject without the tool
// on $PATH — pg's needsPgDump pattern, same reasoning: the lookup comes
// before the check under test, and that order is right for the operator.
func needsMysqldump(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("mysqldump"); err != nil {
		t.Skip("mysqldump is not on $PATH, and this test's subject is only reachable past the lookup")
	}
}

func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	needsMysqldump(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(path, []byte("the backup that already existed"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runDump(context.Background(), req(t, "mysql.dump", map[string]any{
		"database": "app", "out": path, "host": "127.0.0.1", "port": 1,
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mysql.dump.exists" {
		t.Fatalf("err = %v, want mysql.dump.exists", err)
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
	_, err := runDump(context.Background(), req(t, "mysql.dump", map[string]any{
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
	needsMysqldump(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	c := capabilityByID(t, "mysql.dump")
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
	if !strings.Contains(text.Body, "mysqldump") || !strings.Contains(text.Body, path) {
		t.Errorf("dry run = %q, want the command and the destination", text.Body)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("--dry-run created the output file")
	}
}

// classifyDump's branches, each a sentence somebody has stared at.
func TestClassifyDumpNamesTheFailure(t *testing.T) {
	r := req(t, "mysql.dump", map[string]any{"database": "app"})
	boom := errors.New("exit status 2")
	for name, tc := range map[string]struct {
		stderr string
		code   string
	}{
		"auth":      {"mysqldump: Got error: 1045: Access denied for user 'app'@'%'", "mysql.auth.failed"},
		"process":   {"mysqldump: Error: Access denied; you need (at least one of) the PROCESS privilege(s)", "mysql.denied"},
		"no db":     {"mysqldump: Got error: 1049: Unknown database 'app'", "mysql.database.notfound"},
		"refused":   {"mysqldump: Got error: 2002: Can't connect to server", "mysql.conn.refused"},
		"bad host":  {"mysqldump: Got error: 2005: Unknown MySQL server host 'db.internal'", "mysql.host.unknown"},
		"tool skew": {"mysqldump: unknown variable 'ssl-mode=DISABLED'", "mysql.dump.toolskew"},
		"atstraws":  {"mysqldump: something nobody anticipated", "mysql.dump.failed"},
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
