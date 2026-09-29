package plugin

import (
	"path/filepath"
	"testing"
)

// A leading ~ is the person's home; a ~ anywhere else, or one starting a
// name, is part of the path — a file called "~notes" in the working
// directory has to keep working, and "~alice" is not a thing this resolves.
func TestExpandHomeResolvesOnlyALeadingTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for in, want := range map[string]string{
		"~":              home,
		"~/":             home,
		"~/x/y.txt":      filepath.Join(home, "x", "y.txt"),
		"~notes":         "~notes",
		"~alice/x":       "~alice/x",
		"/abs/~/x":       "/abs/~/x",
		"relative/~":     "relative/~",
		"":               "",
		"plain/path.txt": "plain/path.txt",
	} {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

// On Windows "~\" is the home directory as "~/" is, the system's own
// separator after the tilde; elsewhere a backslash is part of a name, and
// "~\x" is a file of that name in the working directory.
func TestExpandHomeTakesTheSystemsOwnSeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, c := range []struct{ goos, in, want string }{
		{"windows", `~\`, home},
		{"windows", `~\x\y.txt`, filepath.Join(home, `x\y.txt`)},
		{"windows", "~/x", filepath.Join(home, "x")},
		{"windows", "~", home},
		{"windows", `~alice\x`, `~alice\x`},
		{"windows", `C:\~\x`, `C:\~\x`},
		{"linux", `~\x`, `~\x`},
		{"darwin", `~\x`, `~\x`},
		{"linux", "~/x", filepath.Join(home, "x")},
	} {
		if got := expandHome(c.goos, c.in); got != c.want {
			t.Errorf("on %s, expandHome(%q) = %q, want %q", c.goos, c.in, got, c.want)
		}
	}
}
