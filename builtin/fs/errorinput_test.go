package fs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// refusedInput is the code of err and the input it names, held to the
// declaration of the capability that answered it: a name the capability does
// not declare is one no surface acts on (plugin.Capability.ErrorInput), so it
// fails here rather than passing as a stamp that does nothing.
func refusedInput(t *testing.T, id string, err error) (code, input string) {
	t.Helper()
	verr := view.AsError(err, "test")
	if verr == nil {
		t.Fatalf("%s answered no error", id)
	}
	if verr.Input != "" {
		var declared bool
		for _, c := range Plugin().Capabilities {
			if c.ID == id {
				_, declared = c.ErrorInput(verr)
			}
		}
		if !declared {
			t.Errorf("%s: %s names the input %q, which %s does not declare", id, verr.Code, verr.Input, id)
		}
	}
	return verr.Code, verr.Input
}

// A refusal of the one input a call got wrong names it, so the form the call
// came from opens again on that box and an agent knows the argument to send
// again: a path that is not there or is the wrong kind of thing, an algorithm
// there is none of, an expected checksum that cannot be one.
//
// A checksum whole for another algorithm names neither: it and algo disagree,
// and either one changed settles it. And a mismatch is the answer to the
// question asked, not a value refused.
func TestARefusalOfOneInputNamesIt(t *testing.T) {
	root := fixture(t, map[string]int{"f.bin": 4})
	file := filepath.Join(root, "f.bin")
	missing := filepath.Join(root, "missing")
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("another file"))

	runs := map[string]plugin.Handler{"fs.tree": runTree, "fs.usage": runUsage, "fs.hash": runHash}
	for _, c := range []struct {
		name, id string
		values   map[string]any
		surface  plugin.Surface
		code     string
		input    string
	}{
		{"a tree of nothing", "fs.tree", map[string]any{"path": missing},
			plugin.SurfaceTUI, "fs.tree.notfound", "path"},
		{"a scan of nothing", "fs.usage", map[string]any{"path": missing},
			plugin.SurfaceCLI, "fs.usage.notfound", "path"},
		{"a hash of nothing", "fs.hash", map[string]any{"path": missing},
			plugin.SurfaceMCP, "fs.hash.notfound", "path"},
		{"a tree of a file", "fs.tree", map[string]any{"path": file},
			plugin.SurfaceTUI, "fs.tree.notadir", "path"},
		{"a tree of a path through a file", "fs.tree", map[string]any{"path": filepath.Join(file, "x")},
			plugin.SurfaceCLI, "fs.tree.notfound", "path"},
		{"a scan of a path through a file", "fs.usage", map[string]any{"path": filepath.Join(file, "x")},
			plugin.SurfaceTUI, "fs.usage.notfound", "path"},
		{"a hash of a path through a file", "fs.hash", map[string]any{"path": filepath.Join(file, "x")},
			plugin.SurfaceCLI, "fs.hash.notfound", "path"},
		{"a scan of a file", "fs.usage", map[string]any{"path": file},
			plugin.SurfaceCLI, "fs.usage.notadir", "path"},
		{"a hash of a directory", "fs.hash", map[string]any{"path": root},
			plugin.SurfaceTUI, "fs.hash.isdir", "path"},
		{"a hash of a pipe", "fs.hash", map[string]any{"path": fifo},
			plugin.SurfaceTUI, "fs.hash.notafile", "path"},
		{"an algorithm there is none of", "fs.hash", map[string]any{"path": file, "algo": "sha3"},
			plugin.SurfaceCLI, "fs.hash.algo", "algo"},
		{"an expect holding no checksum", "fs.hash", map[string]any{"path": file, "expect": "sha256:"},
			plugin.SurfaceTUI, "fs.hash.expect", "expect"},
		{"an expect that is not hex", "fs.hash", map[string]any{"path": file, "expect": "not-a-sum"},
			plugin.SurfaceCLI, "fs.hash.expect", "expect"},
		{"an expect cut short", "fs.hash", map[string]any{"path": file, "expect": strings.Repeat("a", 63)},
			plugin.SurfaceTUI, "fs.hash.expect", "expect"},
		{"an expect whole for another algorithm", "fs.hash",
			map[string]any{"path": file, "algo": "sha256", "expect": strings.Repeat("a", 128)},
			plugin.SurfaceTUI, "fs.hash.expect", ""},
		{"a file that is not the one described", "fs.hash",
			map[string]any{"path": file, "algo": "sha256", "expect": hex.EncodeToString(other[:])},
			plugin.SurfaceCLI, "fs.hash.mismatch", ""},
	} {
		_, err := runs[c.id](context.Background(),
			plugin.NewRequest(c.values, false, false).WithSurface(c.surface))
		code, input := refusedInput(t, c.id, err)
		if code != c.code || input != c.input {
			t.Errorf("%s: %s naming %q, want %s naming %q (%v)", c.name, code, input, c.code, c.input, err)
		}
	}
}

// A path that is there and that the operating system will not let rta read,
// or that fails some other way, names no input: the value is right, and the
// fix is a chmod or the machine, not the box.
func TestAPathThatIsThereButCannotBeReadNamesNoInput(t *testing.T) {
	for _, err := range []error{
		&os.PathError{Op: "open", Path: "/var/log/secure", Err: os.ErrPermission},
		&os.PathError{Op: "open", Path: "/var/log/secure", Err: syscall.EIO},
	} {
		for _, code := range []string{"fs.tree", "fs.hash", "fs.usage"} {
			if verr := pathError(code, "/var/log/secure", err); verr.Input != "" {
				t.Errorf("%s of %v: names the input %q, want none", verr.Code, err, verr.Input)
			}
		}
	}
}
