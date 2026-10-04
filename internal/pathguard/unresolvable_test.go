package pathguard

import (
	"os"
	"path/filepath"
	"testing"
)

// A link the guard cannot follow to its end — its target missing, or behind
// a directory this process may not search — was judged as its own path,
// which is inside the root, while one whose target was there was judged by
// the target and refused. So the answer said whether a file outside the roots
// exists: a caller who may write inside a root makes a link to the name it
// wants to ask about and reads "allowed" as "not there". Each pair here is
// the same link, in the same place, once with its target present and once
// without, and the two answers must be one.
func TestAnUnresolvableLinkIsJudgedByWhereItPoints(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	g, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(root, outside)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		// target is what the link holds, and present makes it exist (or
		// not) before each judgement.
		target  string
		present func(bool)
		// named is what the caller gives, relative to the root.
		named string
	}{
		{name: "a link to a file", target: filepath.Join(outside, "id_rsa"),
			present: file(t, filepath.Join(outside, "id_rsa")), named: "key"},
		{name: "a relative link", target: filepath.Join(rel, "token"),
			present: file(t, filepath.Join(outside, "token")), named: "key"},
		{name: "a link to a directory, and a file under it", target: filepath.Join(outside, "ssh"),
			present: dir(t, filepath.Join(outside, "ssh"), "id_ed25519"), named: filepath.Join("key", "id_ed25519")},
		{name: "a link through a directory that may not be searched",
			target:  filepath.Join(outside, "locked", "secret"),
			present: searchable(t, filepath.Join(outside, "locked"), "secret"), named: "key"},
		{name: "a link to a loop", target: filepath.Join(outside, "loop-a"),
			present: loop(t, filepath.Join(outside, "loop-a"), filepath.Join(outside, "loop-b")), named: "key"},
	} {
		t.Run(c.name, func(t *testing.T) {
			link := filepath.Join(root, "key")
			_ = os.Remove(link)
			if err := os.Symlink(c.target, link); err != nil {
				t.Fatal(err)
			}
			answer := func(present bool) string {
				c.present(present)
				_, verr := g.Check("path", filepath.Join(root, c.named))
				if verr == nil {
					return "allowed"
				}
				return verr.Code + ": " + verr.Message + " / " + verr.Hint
			}
			there, gone := answer(true), answer(false)
			if there != gone {
				t.Errorf("the answer tells whether the target exists:\n  present: %s\n  missing: %s", there, gone)
			}
			if there == "allowed" {
				t.Errorf("a link out of the root was allowed")
			}
		})
	}
}

// A loop never ends anywhere, and the kernel refuses to open one: it is
// refused as unresolvable rather than judged as the path it started from.
func TestALinkLoopIsUnresolvable(t *testing.T) {
	g, root := rooted(t)
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", a); verr == nil || verr.Code != "core.mcp.path.unresolvable" {
		t.Errorf("a loop: %v, want core.mcp.path.unresolvable", verr)
	}
}

// A dangling link that points inside the root is judged there, and allowed:
// "--out" naming a link to a file not written yet is an ordinary thing to do.
func TestADanglingLinkInsideTheRootIsAllowedWhereItPoints(t *testing.T) {
	g, root := rooted(t)
	link := filepath.Join(root, "latest")
	if err := os.Symlink(filepath.Join("reports", "2026.md"), link); err != nil {
		t.Fatal(err)
	}
	got, verr := g.Check("path", link)
	if verr != nil {
		t.Fatalf("a dangling link inside the root was refused: %v", verr)
	}
	want := filepath.Join(g.Roots()[0], "reports", "2026.md")
	if got != want {
		t.Errorf("judged %q, want where the link points, %q", got, want)
	}
}

// file makes a toggle for a file at path: there, or not.
func file(t *testing.T, path string) func(bool) {
	return func(present bool) {
		_ = os.Remove(path)
		if present {
			if err := os.WriteFile(path, []byte("s"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// dir makes a toggle for a directory at path holding name.
func dir(t *testing.T, path, name string) func(bool) {
	return func(present bool) {
		_ = os.RemoveAll(path)
		if present {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, name), []byte("s"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// loop makes a toggle for two links at a and b that lead to each other.
func loop(t *testing.T, a, b string) func(bool) {
	return func(present bool) {
		_ = os.Remove(a)
		_ = os.Remove(b)
		if present {
			if err := os.Symlink(b, a); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(a, b); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// searchable makes a toggle for a directory at path holding name, which may
// be searched, or may not.
func searchable(t *testing.T, path, name string) func(bool) {
	if os.Geteuid() == 0 {
		t.Skip("root searches every directory")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, name), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
	return func(open bool) {
		mode := os.FileMode(0o700)
		if !open {
			mode = 0
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
}
