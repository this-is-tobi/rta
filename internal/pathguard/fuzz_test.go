package pathguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The one property the gate exists for, held against arbitrary strings: a
// path it allows resolves inside the root and never inside rta's own state.
// The root holds a symlink pointing out of it, because that is the shape
// that beat the first version (see resolve).
func FuzzCheck(f *testing.F) {
	root := f.TempDir()
	data := filepath.Join(f.TempDir(), "rta")
	if err := os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o700); err != nil {
		f.Fatal(err)
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		f.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "grants.key"), []byte("k"), 0o600); err != nil {
		f.Fatal(err)
	}
	if err := os.Symlink(data, filepath.Join(root, "out")); err != nil {
		f.Skip("no symlinks here")
	}
	for _, seed := range []string{
		"file", "dir/sub/x", "../x", "dir/../../x", "out/grants.key", "out/../dir",
		"~/x", "https://example.com/repo.git", "user@host:path", "\\\\host\\share", "//x/y", "", " ",
		root, filepath.Join(root, "out", "grants.key"), data,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		t.Setenv("RTA_DATA_DIR", data)
		g, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		resolved, verr := g.Check("path", raw)
		if verr != nil {
			return
		}
		if strings.TrimSpace(raw) == "" {
			return // "not given" is allowed through untouched
		}
		if !filepath.IsAbs(resolved) {
			t.Fatalf("Check(%q) allowed a relative result %q", raw, resolved)
		}
		if !inside(root, resolved) {
			t.Fatalf("Check(%q) allowed %q, which is outside %s", raw, resolved, root)
		}
		if inside(data, resolved) {
			t.Fatalf("Check(%q) allowed %q, which is inside the data directory", raw, resolved)
		}
	})
}
