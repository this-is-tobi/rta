package plugin

import (
	"os"
	"path/filepath"
	"syscall"
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

// AbsPath is ExpandHome made absolute: a relative path names the file under
// the working directory, a ~ one the file under the home, and an absolute
// one itself, cleaned.
func TestAbsPathIsTheFileAPathNames(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"dump.sql":      filepath.Join(wd, "dump.sql"),
		"./a/../b.sql":  filepath.Join(wd, "b.sql"),
		"~/dumps/x.sql": filepath.Join(home, "dumps", "x.sql"),
		"/abs/x.sql":    "/abs/x.sql",
		"/abs//y/.":     "/abs/y",
	} {
		got, err := AbsPath(in)
		if err != nil || got != want {
			t.Errorf("AbsPath(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
}

// A path that leads nowhere is the box's to fix, and a file that is there and
// cannot be read is nobody's box.
func TestPathInputNamesTheBoxOnlyWhenThePathIsWrong(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}, "file"},
		{&os.PathError{Op: "open", Path: "/x/y", Err: syscall.ENOTDIR}, "file"},
		{&os.PathError{Op: "read", Path: "/x", Err: syscall.EISDIR}, "file"},
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, ""},
		{&os.PathError{Op: "read", Path: "/x", Err: syscall.EIO}, ""},
		{nil, ""},
	} {
		if got := PathInput("file", c.err); got != c.want {
			t.Errorf("PathInput(file, %v) = %q, want %q", c.err, got, c.want)
		}
	}
}
