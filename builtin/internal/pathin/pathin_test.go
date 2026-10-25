package pathin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

func TestReadReturnsAFileWhole(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP, plugin.SurfaceTUI} {
		got, err := Read(on(sf), path, 64)
		if err != nil || string(got) != "127.0.0.1 localhost\n" {
			t.Errorf("%s: got %q, %v", sf, got, err)
		}
	}
}

// Exactly the cap is whole and one byte more is refused, on every surface: a
// file cut at the cap and handed on would be a bundle short of its root, or a
// hosts file short of its last entries, read as a whole one.
func TestReadRefusesAFileOverTheCapAndNamesIt(t *testing.T) {
	dir := t.TempDir()
	at, over := filepath.Join(dir, "at"), filepath.Join(dir, "over")
	if err := os.WriteFile(at, []byte("abcd"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(over, []byte("abcde"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP} {
		if got, err := Read(on(sf), at, 4); err != nil || string(got) != "abcd" {
			t.Errorf("%s at the cap: got %q, %v", sf, got, err)
		}
		_, err := Read(on(sf), over, 4)
		var tooLarge *TooLargeError
		if !errors.As(err, &tooLarge) || !strings.Contains(err.Error(), over) {
			t.Errorf("%s over the cap: err = %v, want a TooLargeError naming the file", sf, err)
		}
	}
}

func TestOpenRefusesADirectoryOffTheCLI(t *testing.T) {
	dir := t.TempDir()
	for _, sf := range []plugin.Surface{plugin.SurfaceMCP, plugin.SurfaceTUI, plugin.SurfaceCompletion} {
		_, _, err := Open(on(sf), dir)
		var notAFile *NotAFileError
		if !errors.As(err, &notAFile) || !strings.Contains(err.Error(), "a directory") {
			t.Errorf("%s: err = %v, want a NotAFileError saying it is a directory", sf, err)
		}
	}
}

// on is a request from sf, with no bounds: the request a CLI or a TUI hands
// a handler.
func on(sf plugin.Surface) plugin.Request {
	return plugin.NewRequest(nil, false, false).WithSurface(sf)
}

// The path is what is wrong when nothing is there, something other than a file
// is, or a file larger than the call reads, and a refusal names its input; a
// file that is there and cannot be read is not, and names none.
func TestOnlyAWrongPathNamesItsInput(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.ENOENT}, "file"},
		{&os.PathError{Op: "open", Path: "/x/y", Err: syscall.ENOTDIR}, "file"},
		{&os.PathError{Op: "read", Path: "/x", Err: syscall.EISDIR}, "file"},
		{&NotAFileError{Path: "/x", Mode: fs.ModeNamedPipe}, "file"},
		{&TooLargeError{Path: "/x", Max: 1}, "file"},
		{&os.PathError{Op: "read", Path: "/x", Err: atomicfile.ErrNotRegular}, "file"},
		{&os.PathError{Op: "open", Path: "/x", Err: syscall.EACCES}, ""},
		{&os.PathError{Op: "read", Path: "/x", Err: syscall.EIO}, ""},
		{errors.New("no path in it"), ""},
	} {
		if got := Input("file", c.err); got != c.want {
			t.Errorf("Input(file, %v) = %q, want %q", c.err, got, c.want)
		}
	}
}
