package plugin

import (
	"path/filepath"
	"testing"
)

// A leading ~ is the person's home; a ~ anywhere else, or one starting a
// name, is part of the path — a file called "~notes" in the working
// directory has to keep working, and "~alice" is not a thing this resolves.
// A backslash is part of a name, so "~\x" is a file of that name.
func TestExpandHomeResolvesOnlyALeadingTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for in, want := range map[string]string{
		"~":              home,
		"~/":             home,
		"~/x/y.txt":      filepath.Join(home, "x", "y.txt"),
		"~notes":         "~notes",
		"~alice/x":       "~alice/x",
		`~\x`:            `~\x`,
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
