package pathguard

import (
	"os"
	"path/filepath"
	"testing"
)

// A path is opened from the deepest root it lies under. A root drawn inside
// another is the one meant for what is under it, and it may be readable
// where the outer one is not: os.Root goes down a directory by opening it,
// so an outer root the server may search but not list (mode --x) failed
// every path under the inner root too whenever --root named the outer first.
func TestAPathIsOpenedFromTheDeepestRootItLiesUnder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a directory whatever its mode")
	}
	t.Setenv("RTA_DATA_DIR", filepath.Join(t.TempDir(), "data"))
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	if err := os.MkdirAll(inner, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(inner, "notes.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(outer, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(outer, 0o700) })

	for _, roots := range [][]string{{outer, inner}, {inner, outer}} {
		g, err := New(roots...)
		if err != nil {
			t.Fatal(err)
		}
		root, rel, err := g.Bounds().Root(file)
		if err != nil {
			t.Errorf("roots %v: %v, want the file opened from the inner root", roots, err)
			continue
		}
		data, err := root.ReadFile(rel)
		_ = root.Close()
		if err != nil || string(data) != "hello" {
			t.Errorf("roots %v: read %q, %v", roots, data, err)
		}
	}
}
