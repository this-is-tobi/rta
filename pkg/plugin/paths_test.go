package plugin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

// ShortHome is ExpandHome's inverse, and only for a path that is the home or
// under it. audit.agents' copy took any path that began with the home's
// letters: /home/tobi-work beside a home of /home/tobi became ~-work, and a
// home of /, which Docker gives a --user with no passwd entry, put ~ in front
// of every absolute path, ~usr/local/bin/rta.
func TestShortHomeSpellsOnlyTheHomeAndWhatIsUnderItWithATilde(t *testing.T) {
	for _, tc := range []struct{ path, home, want string }{
		{"/home/tobi/.mcp.json", "/home/tobi", "~/.mcp.json"},
		{"/home/tobi", "/home/tobi", "~"},
		{"/home/tobi/x", "/home/tobi/", "~/x"},
		{"/home/tobi-work/.mcp.json", "/home/tobi", "/home/tobi-work/.mcp.json"},
		{"/home/tobiias/x", "/home/tobi", "/home/tobiias/x"},
		{"/usr/local/bin/rta", "/", "/usr/local/bin/rta"},
		{"/home/tobi/x", "", "/home/tobi/x"},
		{"relative/x", "/home/tobi", "relative/x"},
	} {
		if got := ShortHome(tc.path, tc.home); got != tc.want {
			t.Errorf("ShortHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
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

// A path is completed from the directory typed so far, a directory with its
// slash, a dotfile only once its dot is typed, and ~ kept as it was typed.
func TestSuggestPathCompletesTheDirectoryBeingTyped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"ca.pem", "client.key", ".hidden"} {
		if err := os.WriteFile(filepath.Join(home, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(home, "certs"), 0o700); err != nil {
		t.Fatal(err)
	}
	suggest := SuggestPath("ca-file")
	typed := func(v string) []string {
		return suggest(context.Background(), NewRequest(map[string]any{"ca-file": v}, false, false))
	}
	if got := typed("~/c"); !reflect.DeepEqual(got, []string{"~/ca.pem", "~/certs/", "~/client.key"}) {
		t.Errorf("~/c: %v", got)
	}
	if got := typed(home + "/."); !reflect.DeepEqual(got, []string{home + "/.hidden"}) {
		t.Errorf("a typed dot: %v", got)
	}
	if got := typed(home + "/missing/x"); got != nil {
		t.Errorf("a directory that is not there: %v", got)
	}
}
