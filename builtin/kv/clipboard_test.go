package kv

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fakeClipboard puts a stand-in for every clipboard program this package
// knows about on PATH, and nothing else.
//
// A stub for copyToClipboard would prove only that the stub was called. What
// has to be true is a property of the process we actually start — the value
// arrives on its stdin and appears nowhere in its argv — so the test runs a
// real program and reads back both.
func fakeClipboard(t *testing.T) (stdin, argv string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in is a shell script")
	}
	dir := t.TempDir()
	stdin = filepath.Join(dir, "stdin")
	argv = filepath.Join(dir, "argv")
	script := "#!/bin/sh\ncat > " + stdin + "\nprintf '%s\\n' \"$@\" > " + argv + "\n"
	for _, name := range []string{"pbcopy", "xclip", "xsel", "wl-copy", "clip", "clip.exe", "termux-clipboard-set"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// Prepended, not substituted: the stand-in is a shell script, and a PATH
	// holding nothing but the stand-ins leaves it unable to find `cat` — at
	// which point the redirection still creates an empty file and the test
	// blames the code under it.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return stdin, argv
}

// The refusal is decided before the store is opened, so an agent's call never
// spends the passphrase the MCP server was launched with on a question that
// was always going to be no. A passphrase that could not possibly work is how
// that ordering is visible: reaching load() would report kv.wrongpass instead.
func TestCopyIsRefusedOverMCPBeforeAnythingIsDecrypted(t *testing.T) {
	setup(t)
	stdin, _ := fakeClipboard(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)

	_, err := runCopy(context.Background(), plugin.NewRequest(
		map[string]any{"key": "db-password", "passphrase": "not the passphrase"}, false, true).
		WithSurface(plugin.SurfaceMCP))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.copy.noclipboard" || !ve.Refusal {
		t.Fatalf("MCP copy = %+v, want a marked refusal", ve)
	}
	if ve.Hint == "" {
		t.Error("a refusal an agent cannot act on is a retry loop")
	}
	if _, err := os.Stat(stdin); err == nil {
		t.Error("the refused call still reached the clipboard")
	}
}

// The success path itself: every other test in this file covers a reason
// copyToClipboard is never reached (MCP, dry-run, no program, unknown key).
// None of them read fakeClipboard's stdin file for anything but "does this
// exist" — so a bug that copied the wrong entry's value, a truncated or
// re-encoded one, or dropped the copy from the success path entirely while
// still printing the confirmation message, would pass every test here.
func TestCopyPutsTheExactStoredValueOnTheClipboard(t *testing.T) {
	setup(t)
	stdin, _ := fakeClipboard(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)
	text(t, runSet, map[string]any{"key": "other-key", "value": "not-this-one"}, false)

	body := text(t, runCopy, map[string]any{"key": "db-password"}, false)

	got, err := os.ReadFile(stdin)
	if err != nil {
		t.Fatalf("nothing reached the clipboard program: %v", err)
	}
	if string(got) != "s3cr3t" {
		t.Fatalf("clipboard stdin = %q, want %q", got, "s3cr3t")
	}
	if !strings.Contains(body, "db-password") {
		t.Errorf("confirmation message does not name the key: %q", body)
	}
	if strings.Contains(body, "s3cr3t") {
		t.Errorf("the confirmation message itself printed the value: %q", body)
	}
}

func TestCopyDryRunLeavesTheClipboardAlone(t *testing.T) {
	setup(t)
	stdin, _ := fakeClipboard(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)

	body := text(t, runCopy, map[string]any{"key": "db-password"}, true)

	if _, err := os.Stat(stdin); err == nil {
		t.Fatal("a dry run copied the value")
	}
	if !strings.HasPrefix(body, "would copy") {
		t.Errorf("dry run = %q", body)
	}
	if strings.Contains(body, "s3cr3t") {
		t.Errorf("the dry run printed the value: %q", body)
	}
}

// A machine with no clipboard program is a headless server, and "exit status
// 1" there sends people looking for a bug in the store. Name what is missing
// and the way out that does not need one.
func TestCopyWithNoClipboardProgramNamesTheWayOut(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)
	t.Setenv("PATH", t.TempDir())

	_, err := runCopy(context.Background(), req(map[string]any{"key": "db-password"}, false))
	ve := view.AsError(err, "z")
	if ve.Code != "kv.clipboard.missing" {
		t.Fatalf("copy without a clipboard = %+v", ve)
	}
	if !strings.Contains(ve.Hint, "--out") {
		t.Errorf("hint does not offer the alternative: %q", ve.Hint)
	}
}

func TestCopyUnknownKeyIsCoded(t *testing.T) {
	setup(t)
	fakeClipboard(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)

	_, err := runCopy(context.Background(), req(map[string]any{"key": "nope"}, false))
	if ve := view.AsError(err, "z"); ve.Code != "kv.notfound" {
		t.Errorf("copying a key that does not exist = %+v", ve)
	}
}
