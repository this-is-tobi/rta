package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **argv is world-readable through `ps`.** A database password on a child's
// command line is visible to every account on the machine for as long as the
// dump runs, which for a real database is minutes. It goes through the
// environment instead, and this is the test that keeps it there — the same
// class of mistake as building a DSN by string concatenation, which this
// plugin already refuses to do.
func TestThePasswordNeverReachesTheCommandLine(t *testing.T) {
	r := reqFor(t, "pg.dump", map[string]any{
		"host": "db.internal", "port": 6543, "user": "app",
		"database": "prod", "password": "hunter2",
	})
	for _, arg := range dumpArgs(r) {
		if strings.Contains(arg, "hunter2") {
			t.Fatalf("the password is in argv: %q", arg)
		}
	}
	joined := strings.Join(dumpArgs(r), " ")
	for _, want := range []string{"--host=db.internal", "--port=6543",
		"--username=app", "--dbname=prod"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv is missing %q: %s", want, joined)
		}
	}
}

// Without --no-password, a missing credential makes pg_dump prompt on a
// terminal the plugin does not own, and the call hangs until somebody kills
// it. Failing immediately is what a wrapper owes its caller.
func TestTheChildCanNeverStopAtAPrompt(t *testing.T) {
	r := reqFor(t, "pg.dump", map[string]any{})
	if !strings.Contains(strings.Join(dumpArgs(r), " "), "--no-password") {
		t.Error("pg_dump is run without --no-password, so a missing password hangs the call")
	}
}

func TestTheIncludeAndFormatFlagsMap(t *testing.T) {
	for _, tc := range []struct {
		include, format, want, absent string
	}{
		{"all", "plain", "--format=plain", "--schema-only"},
		{"schema", "plain", "--schema-only", "--data-only"},
		{"data", "plain", "--data-only", "--schema-only"},
		{"all", "custom", "--format=custom", "--format=plain"},
	} {
		got := strings.Join(dumpArgs(reqFor(t, "pg.dump", map[string]any{
			"include": tc.include, "format": tc.format,
		})), " ")
		if !strings.Contains(got, tc.want) {
			t.Errorf("include=%s format=%s: missing %q in %q", tc.include, tc.format, tc.want, got)
		}
		if strings.Contains(got, tc.absent) {
			t.Errorf("include=%s format=%s: unexpected %q in %q", tc.include, tc.format, tc.absent, got)
		}
	}
}

// needsPgDump skips a test that cannot reach its own subject without pg_dump
// on $PATH.
//
// runFullDump looks the tool up before it checks anything else, and that order
// is right for the operator: "no pg_dump on $PATH" is the blocker to fix
// first, and reversing it would send somebody to move a file and then meet the
// real problem anyway. It does mean these two tests assert on a refusal that
// happens *after* the lookup, so without the tool they fail on a message about
// something else entirely — which is a broken test, not a broken product, and
// is how it failed on a CI runner with no postgres client installed while
// passing on every developer machine that has one.
func needsPgDump(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("pg_dump is not on $PATH, and this test's subject is only reachable past the lookup")
	}
}

// **A dump is never written over an existing file**, the same discipline
// keys.restore applies to a restored key. O_EXCL rather than a stat followed
// by a create, because a backup should not have that race.
func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	needsPgDump(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	if err := os.WriteFile(path, []byte("the backup that already existed"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runFullDump(context.Background(),
		reqFor(t, "pg.dump", map[string]any{"out": path, "host": "127.0.0.1", "port": 1}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "pg.dump.exists" {
		t.Fatalf("err = %v, want pg.dump.exists", err)
	}
	// And it is still there, unread and unchanged.
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "the backup that already existed" {
		t.Errorf("the existing file was disturbed: %q, %v", body, err)
	}
}

// A run that fails takes its half-written file with it. A partial dump left
// on disk is the one that gets restored six months later.
func TestAFailedDumpLeavesNoFileBehind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.sql")

	// Nothing is listening on port 1, so pg_dump exits non-zero after the
	// file has already been created.
	_, err := runFullDump(context.Background(), reqFor(t, "pg.dump", map[string]any{
		"out": path, "host": "127.0.0.1", "port": 1, "database": "app",
	}))
	if err == nil {
		t.Skip("a dump against a closed port unexpectedly succeeded")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("a failed dump left its partial file on disk, where it reads as a good one")
	}
}

// **The receipt has to measure the file, not the writer.**
//
// The first version read the descriptor's offset after the child exited, on
// the reasoning that the child inherits this exact descriptor and so shares
// its position. It does — and pg_dump *seeks*: custom format writes the
// archive and then goes back to patch the table-of-contents offsets, leaving
// the shared offset near the start. A 219 MB dump reported itself as 6.6 KiB,
// which is exactly the kind of wrong that looks plausible on a receipt nobody
// re-measures. Found by dumping something big enough for the number to be
// obviously absurd.
func TestTheSizeIsTheFileNotTheWritersPosition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seeky.dump")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(make([]byte, 4096)); err != nil {
		t.Fatal(err)
	}
	// What pg_dump does at the end of a custom-format dump: go back and patch
	// the header, leaving the offset far from the end of the file.
	if _, err := f.Seek(16, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("toc")); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if got := sizeOnDisk(path); got != 4096 {
		t.Errorf("size = %d, want 4096 — the writer's offset when it finished was 19", got)
	}
}

// A directory-format dump is many files, and the size is all of them.
func TestTheSizeOfADirectoryDumpIsEveryFileInIt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"toc.dat", "3241.dat.gz", "3242.dat.gz"} {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, 1000), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := sizeOnDisk(dir); got != 3000 {
		t.Errorf("size = %d, want 3000", got)
	}
}

// **--jobs is refused where it cannot work, rather than silently switching
// the format.** pg_dump parallelises per table into per-table files, so it is
// directory format or nothing; changing the format under somebody would hand
// them a directory where they asked for a file and change the restore command
// they were about to be given.
func TestParallelIsRefusedWhereItCannotWork(t *testing.T) {
	for _, format := range []string{"plain", "custom"} {
		verr := checkParallel(reqFor(t, "pg.dump", map[string]any{
			"format": format, "jobs": 8,
		}))
		if verr == nil {
			t.Fatalf("format %s: --jobs 8 was accepted", format)
		}
		if verr.Code != "pg.dump.notparallel" {
			t.Errorf("format %s: code = %q", format, verr.Code)
		}
		if !strings.Contains(verr.Hint, "--format directory --jobs 8") {
			t.Errorf("format %s: hint = %q, want the working command", format, verr.Hint)
		}
	}

	if verr := checkParallel(reqFor(t, "pg.dump", map[string]any{
		"format": "directory", "jobs": 8,
	})); verr != nil {
		t.Errorf("directory format refused --jobs: %v", verr)
	}
	// One worker is the serial case and is fine in any format.
	if verr := checkParallel(reqFor(t, "pg.dump", map[string]any{
		"format": "custom", "jobs": 1,
	})); verr != nil {
		t.Errorf("the serial case was refused: %v", verr)
	}
}

// --jobs reaches pg_dump, and carries into the pg_restore command the receipt
// prints — the half people forget, since a restore rebuilds every index and
// is usually the slower direction.
func TestParallelReachesBothTheDumpAndTheRestore(t *testing.T) {
	r := reqFor(t, "pg.dump", map[string]any{
		"format": "directory", "jobs": 6, "database": "app",
		"host": "db.internal", "port": 5432,
	})
	if got := strings.Join(dumpArgs(r), " "); !strings.Contains(got, "--jobs=6") {
		t.Errorf("argv has no --jobs: %s", got)
	}
	restore := restoreCommand(r, "/backups/app")
	if !strings.HasPrefix(restore, "pg_restore ") || !strings.Contains(restore, "--jobs=6") {
		t.Errorf("restore = %q, want a parallel pg_restore", restore)
	}
	// And the serial case says nothing about jobs rather than saying --jobs=1.
	serial := restoreCommand(reqFor(t, "pg.dump", map[string]any{
		"format": "directory", "jobs": 1, "database": "app",
	}), "/backups/app")
	if strings.Contains(serial, "--jobs") {
		t.Errorf("serial restore = %q, want no --jobs", serial)
	}
}

// --dry-run writes nothing, which is the host's promise for any write
// capability and one that a capability creating a file has to keep
// explicitly. plugins/s3's object.get is the counter-example in this
// codebase: it writes on --dry-run.
func TestDryRunCreatesNothing(t *testing.T) {
	needsPgDump(t)
	path := filepath.Join(t.TempDir(), "app.sql")
	v, err := runFullDump(context.Background(),
		dryRunReqFor(t, "pg.dump", map[string]any{"out": path}))
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("--dry-run created the file")
	}
	text, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(text.Body, path) {
		t.Errorf("the dry run does not say where it would write: %q", text.Body)
	}
}

// Saying where the dump goes is required, and the refusal names the flag and
// a filename rather than only reporting that something is missing.
func TestNoDestinationIsRefusedWithTheFlagNamed(t *testing.T) {
	_, err := runFullDump(context.Background(),
		reqFor(t, "pg.dump", map[string]any{"database": "prod"}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "pg.dump.nooutput" {
		t.Fatalf("err = %v, want pg.dump.nooutput", err)
	}
	if !strings.Contains(verr.Hint, "--out ./prod.sql") {
		t.Errorf("hint = %q, want it to show the flag with a usable filename", verr.Hint)
	}
}

// **A backup capability that does not say how to restore is the shape of
// every backup that turned out not to be one.** The command depends on the
// format, and getting it wrong is a person discovering at the worst possible
// moment that psql cannot read a custom-format archive.
func TestTheRestoreCommandMatchesTheFormat(t *testing.T) {
	plain := restoreCommand(reqFor(t, "pg.dump", map[string]any{
		"format": "plain", "database": "app", "host": "db.internal", "port": 5432,
	}), "/backups/app.sql")
	if !strings.HasPrefix(plain, "psql ") || !strings.Contains(plain, "--file=/backups/app.sql") {
		t.Errorf("plain restore = %q, want a psql command", plain)
	}

	custom := restoreCommand(reqFor(t, "pg.dump", map[string]any{
		"format": "custom", "database": "app", "host": "db.internal", "port": 5432,
	}), "/backups/app.dump")
	if !strings.HasPrefix(custom, "pg_restore ") {
		t.Errorf("custom restore = %q, want a pg_restore command", custom)
	}
}

// The failure nobody guesses from the message, because it reads like a
// server problem and is a client one.
func TestAVersionMismatchIsNamedAsAClientProblem(t *testing.T) {
	stderr := "pg_dump: error: server version: 17.11; pg_dump version: 15.4\n" +
		"pg_dump: error: aborting because of server version mismatch\n"
	verr := classifyDump(errors.New("exit status 1"), stderr,
		reqFor(t, "pg.dump", map[string]any{}))
	if verr.Code != "pg.dump.version" {
		t.Fatalf("code = %q, want pg.dump.version", verr.Code)
	}
	if !strings.Contains(verr.Hint, "newer than itself") {
		t.Errorf("hint = %q, want it to say which side is too old", verr.Hint)
	}
}

// pg_dump's own words, not a paraphrase of them. rta classifies the failures
// it can name and passes the rest through, so an operator can search for the
// message they were actually given.
func TestAnUnrecognisedFailurePassesTheToolsWordsThrough(t *testing.T) {
	verr := classifyDump(errors.New("exit status 1"),
		"pg_dump: error: query failed: something specific and unusual\n",
		reqFor(t, "pg.dump", map[string]any{}))
	if verr.Code != "pg.dump.failed" {
		t.Fatalf("code = %q, want pg.dump.failed", verr.Code)
	}
	if !strings.Contains(verr.Message, "something specific and unusual") {
		t.Errorf("message = %q, want pg_dump's own last line", verr.Message)
	}
}
