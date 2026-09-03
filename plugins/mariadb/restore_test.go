package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func dumpOnDisk(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRestoreRefusesMCP(t *testing.T) {
	r := req(t, "mariadb.restore", map[string]any{
		"database": "app", "file": dumpOnDisk(t, "CREATE TABLE t (id int);"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runRestore(context.Background(), r)
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.human" || !verr.Refusal {
		t.Fatalf("err = %v, want mariadb.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "overwrites") {
		t.Errorf("hint = %q, want it to explain the arriving direction", verr.Hint)
	}
}

func TestRestoreRequiresADatabase(t *testing.T) {
	_, err := runRestore(context.Background(), req(t, "mariadb.restore", map[string]any{
		"file": dumpOnDisk(t, "sql"),
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.restore.nodatabase" {
		t.Fatalf("err = %v, want mariadb.restore.nodatabase", err)
	}
}

func TestRestoreRefusesAMissingFile(t *testing.T) {
	_, err := runRestore(context.Background(), req(t, "mariadb.restore", map[string]any{
		"database": "app", "file": filepath.Join(t.TempDir(), "nope.sql"),
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.restore.nofile" {
		t.Fatalf("err = %v, want mariadb.restore.nofile", err)
	}
}

// An empty file restores as nothing and reports success; only rta can say
// "the dump did not finish" — the server just sees no statements.
func TestRestoreRefusesAnEmptyFile(t *testing.T) {
	_, err := runRestore(context.Background(), req(t, "mariadb.restore", map[string]any{
		"database": "app", "file": dumpOnDisk(t, ""),
	}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "mariadb.restore.empty" {
		t.Fatalf("err = %v, want mariadb.restore.empty", err)
	}
}

// **The argv table for the restore direction.** The load-bearing entry is an
// absence: --force is the flag that counts errors quietly to the end and
// calls the survivor a restore, and stop-at-first-error is the only
// guarantee this direction has.
func TestRestoreArgsCarryTheDecidedFlags(t *testing.T) {
	args := strings.Join(restoreArgs(req(t, "mariadb.restore", map[string]any{
		"database": "app", "file": "x.sql",
	})), " ")
	if !strings.HasPrefix(args, "--no-defaults") {
		t.Errorf("--no-defaults is not first: %q", args)
	}
	if !strings.Contains(args, "--local-infile=0") {
		t.Errorf("LOAD DATA LOCAL is not disabled — a dump file could read this machine's "+
			"files into the server: %q", args)
	}
	if strings.Contains(args, "--force") {
		t.Errorf("--force reached argv; stop-at-first-error is the whole guarantee: %q", args)
	}
	if strings.Contains(args, "password") {
		t.Errorf("a password reached argv: %q", args)
	}
	if !strings.HasSuffix(args, " app") {
		t.Errorf("the database does not come last: %q", args)
	}
}

func TestClassifyRestoreNamesTheFailure(t *testing.T) {
	r := req(t, "mariadb.restore", map[string]any{"database": "app"})
	boom := errors.New("exit status 1")
	for name, tc := range map[string]struct {
		stderr string
		code   string
	}{
		"no target": {"ERROR 1049 (42000): Unknown database 'app'", "mariadb.restore.notarget"},
		"auth":      {"ERROR 1045 (28000): Access denied for user 'app'@'%'", "mariadb.auth.failed"},
		"read only": {"ERROR 1290 (HY000): The MySQL server is running with the --read-only option", "mariadb.restore.readonly"},
		"refused":   {"ERROR 2002 (HY000): Can't connect to server on '127.0.0.1'", "mariadb.conn.refused"},
		"mid file":  {"ERROR 1064 (42000) at line 42: You have an error in your SQL syntax", "mariadb.restore.failed"},
		"anything":  {"something nobody anticipated", "mariadb.restore.failed"},
	} {
		t.Run(name, func(t *testing.T) {
			verr := classifyRestore(boom, tc.stderr, r)
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q (stderr %q)", verr.Code, tc.code, tc.stderr)
			}
			if verr.Hint == "" {
				t.Error("no hint")
			}
		})
	}
}

// The mid-file failure names its line, because that is where the operator's
// investigation starts — and its hint owns the partial restore.
func TestAMidFileFailureNamesItsLineAndThePartialState(t *testing.T) {
	verr := classifyRestore(errors.New("exit status 1"),
		"ERROR 1064 (42000) at line 42: You have an error in your SQL syntax",
		req(t, "mariadb.restore", map[string]any{"database": "app"}))
	if !strings.Contains(verr.Message, "at line 42") {
		t.Errorf("message = %q, want the line carried verbatim", verr.Message)
	}
	if !strings.Contains(verr.Hint, "cannot roll") {
		t.Errorf("hint = %q, want it to own that a partial restore remains", verr.Hint)
	}
}
