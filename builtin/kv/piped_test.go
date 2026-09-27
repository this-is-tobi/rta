//go:build !windows

package kv

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// `--file /dev/stdin` is how the CLI chapter says to store a piped secret,
// and it was stored as though it were a file on disk: kind "file", whatever
// it held, and source "file:stdin", naming a file nobody has. A pipe is where
// a value came through, not a file it was kept in, so it is labelled by what
// it holds, the way a typed one is, and its source says it was piped. A named
// pipe stands in for /dev/stdin here, which is a pipe exactly when something
// is piped into the command.
func TestAValuePipedThroughFileIsLabelledByWhatItHolds(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	dir := t.TempDir()
	pipe := func(name, body string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
		go func() {
			w, err := os.OpenFile(p, os.O_WRONLY, 0)
			if err != nil {
				return
			}
			_, _ = w.WriteString(body)
			_ = w.Close()
		}()
		return p
	}
	text(t, runSet, map[string]any{"key": "token", "file": pipe("stdin", "s3cr3t-token")}, false)
	text(t, runSet, map[string]any{"key": "config", "file": pipe("63", `{"user":"a"}`)}, false)
	// Bytes that are not text are a file however they came in, as a DER
	// certificate piped from another command is.
	text(t, runSet, map[string]any{"key": "der", "file": pipe("62", string([]byte{0x30, 0x82, 0xff, 0x00}))}, false)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), []byte("arbitrary bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	text(t, runSet, map[string]any{"key": "blob", "file": filepath.Join(dir, "blob.bin")}, false)
	// `--file /dev/stdin < token.txt`: the descriptor is a regular file, and
	// its name is still no file's.
	redirected, err := os.Open(filepath.Join(dir, "blob.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = redirected.Close() }()
	text(t, runSet, map[string]any{"key": "redirected",
		"file": "/dev/fd/" + strconv.Itoa(int(redirected.Fd()))}, false)

	tbl := table(t, runList, map[string]any{"detail": true})
	keyCol, kindCol, srcCol := col(t, tbl, "Key"), col(t, tbl, "Kind"), col(t, tbl, "Source")
	got := map[string][2]string{}
	for _, row := range tbl.Rows {
		got[row[keyCol]] = [2]string{row[kindCol], row[srcCol]}
	}
	for key, want := range map[string][2]string{
		"token":      {"string", "piped"},
		"config":     {"json", "piped"},
		"der":        {"file", "piped"},
		"redirected": {"string", "piped"},
		// A file on disk is still one: its name is kept, and a value nothing
		// recognises is labelled a file.
		"blob": {"file", "file:blob.bin"},
	} {
		if got[key] != want {
			t.Errorf("%s: kind and source = %q, want %q", key, got[key], want)
		}
	}
	s, verr := load(req(nil, false))
	if verr != nil {
		t.Fatal(verr)
	}
	if f := s.Entries["token"].Filename; f != "" {
		t.Errorf("a piped value recorded the file name %q, which kv edit reads as the file it came from", f)
	}
	if v := string(s.Entries["token"].Value); v != "s3cr3t-token" {
		t.Errorf("the piped value was stored as %q", v)
	}
}
