package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A root the server can search and not read — mode --x — refuses every read
// bounded under it, since os.Root opens a directory for reading. It is said
// once, at the start, naming the root and the fix, and a root it can read is
// not mentioned.
func TestAServerSaysWhichRootItCannotRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode says")
	}
	readable := t.TempDir()
	searchOnly := filepath.Join(t.TempDir(), "search-only")
	if err := os.Mkdir(searchOnly, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(searchOnly, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(searchOnly, 0o700) })

	if lines := unreadableRoots([]string{readable}); len(lines) != 0 {
		t.Errorf("a readable root was said to be unreadable: %q", lines)
	}
	lines := unreadableRoots([]string{readable, searchOnly})
	if len(lines) != 1 {
		t.Fatalf("said %q, want one line about %s", lines, searchOnly)
	}
	for _, want := range []string{"the root " + searchOnly + " cannot be read (permission denied)",
		"no built-in can open a path through it", "chmod u+r " + searchOnly, "--root"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the line does not say %q: %s", want, lines[0])
		}
	}
}
