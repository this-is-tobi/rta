package pathin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/pathguard/pathswap"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// sealKey is what rta's grants.key holds in these tests, and what no answer
// under a root may hold.
const sealKey = "the seal key for every grant"

// ownState puts rta's data directory at data, with grants.key in it, and
// its configuration out of the way; the key's path comes back.
func ownState(t *testing.T, data string) string {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	key := filepath.Join(data, "grants.key")
	put(t, key, sealKey)
	return key
}

// hardLink makes at another name for target, or skips where the volume
// makes none.
func hardLink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Link(target, at); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}
}

// protectedErr reports whether err is the refusal of rta's own state, as
// the name rule words it.
func protectedErr(err error) bool {
	var withheld *WithheldError
	var verr *view.Error
	return errors.As(err, &withheld) && errors.As(err, &verr) && verr.Code == "core.mcp.path.protected"
}

// A hard link under a root to a file of rta's state is another name for the
// same file, and judged by that name it was a note in a project: every way
// of opening it read the seal key. It is refused by the file's identity, as
// the file's own name is refused, though the data directory is nowhere near
// the root.
func TestAHardLinkToRtasOwnStateIsRefused(t *testing.T) {
	root := t.TempDir()
	key := ownState(t, filepath.Join(t.TempDir(), "data"))
	link := filepath.Join(root, "notes.txt")
	hardLink(t, key, link)
	req := under(t, root)

	if got, err := Read(req, link, 64); !protectedErr(err) {
		t.Errorf("Read of a hard link to grants.key: %q, %v, want it refused as rta's own state", got, err)
	}
	if _, err := Stat(req, link); !protectedErr(err) {
		t.Errorf("Stat of it: %v, want it refused as rta's own state", err)
	}
	if got, err := fs.ReadFile(FS(req, root), "notes.txt"); !protectedErr(err) {
		t.Errorf("a scan's read of it: %q, %v, want it refused as rta's own state", got, err)
	}
	// A person at a terminal reads their own files, by any name.
	if got, err := Read(on(plugin.SurfaceCLI), link, 64); err != nil || string(got) != sealKey {
		t.Errorf("the CLI's read of the operator's own link: %q, %v", got, err)
	}
}

// A walk under a root that reaches a hard link to rta's state withholds it
// as it withholds the data directory, and opens it through no way a walk
// has: listed, opened as a file, or opened from a directory it was reached
// by.
func TestAWalkWithholdsAHardLinkToRtasOwnState(t *testing.T) {
	root := t.TempDir()
	key := ownState(t, filepath.Join(t.TempDir(), "data"))
	sub := filepath.Join(root, "project")
	put(t, filepath.Join(sub, "README.md"), "a project")
	hardLink(t, key, filepath.Join(sub, "cache.bin"))
	req := under(t, root)

	d, err := OpenDir(req, sub)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir()
	if err != nil {
		t.Fatal(err)
	}
	withheld := map[string]bool{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		withheld[e.Name()] = d.Withheld(e.Name(), info) != nil
	}
	if !withheld["cache.bin"] || withheld["README.md"] {
		t.Errorf("withheld = %v, want the link to grants.key alone", withheld)
	}
	if f, _, err := d.OpenFile("cache.bin"); !protectedErr(err) {
		if f != nil {
			_ = f.Close()
		}
		t.Errorf("a walk opened the link to grants.key: %v", err)
	}
	if f, _, err := d.OpenFile("README.md"); err != nil {
		t.Errorf("a walk's open of a file beside it: %v", err)
	} else {
		_ = f.Close()
	}
}

// A caller who can write inside a root that holds rta's state can move a
// file of it onto a name the guard has judged, and back: no link is made and
// the name opened is the name judged, so every rule by name passes it. What
// is opened is refused by identity, whenever the move lands — before the
// call, between the judging and the open, or while it runs.
func TestAFileOfRtasStateMovedOntoAJudgedNameIsNotRead(t *testing.T) {
	root := t.TempDir()
	key := ownState(t, filepath.Join(root, ".local", "share", "rta"))
	file := filepath.Join(root, "notes.txt")
	put(t, file, "a note")
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	judged, verr := g.Check("path", file)
	if verr != nil {
		t.Fatal(verr)
	}
	call := func() pathswap.Outcome {
		// A request per call, with bounds of its own, as the bridge makes
		// one.
		got, err := Read(on(plugin.SurfaceMCP).WithBounds(g.Bounds()), judged, 64)
		return pathswap.Outcome{Out: string(got), Err: err}
	}
	before := call()
	if before.Err != nil || before.Out != "a note" {
		t.Fatalf("before any move: %q, %v", before.Out, before.Err)
	}
	pathswap.Rename(t, key, file)
	pathswap.Run(t, before.Out, call, func(o pathswap.Outcome) bool { return strings.Contains(o.Out, "seal key") })
}

// rta's whole data directory moved under a root by another name — one
// rename, by a caller who can write in the directory holding it — is refused
// whole, and the files in it rta wrote to after the server started with it:
// none of them is in the guard's first reading as it is now, and the call's
// own looks where the directory was. Each is refused as another name for the
// state, the words an operator needs to find what was moved.
func TestRtasDataDirectoryMovedUnderARootIsRefusedWhole(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, ".local", "share", "rta")
	key := ownState(t, data)
	req := under(t, root)
	f, err := os.OpenFile(key, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(", written to since")
	if cerr := f.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
	// And a file rta writes anew, as it rewrites its grant records, which
	// changes the directory as well.
	put(t, filepath.Join(data, "grants.json"), sealKey)
	later := time.Now().Add(time.Hour)
	for _, p := range []string{key, data} {
		if err := os.Chtimes(p, later, later); err != nil {
			t.Fatal(err)
		}
	}
	moved := filepath.Join(root, "backup")
	if err := os.Rename(data, moved); err != nil {
		t.Fatal(err)
	}

	if got, err := Read(req, filepath.Join(moved, "grants.key"), 256); !protectedErr(err) {
		t.Errorf("Read of grants.key in the moved directory: %q, %v, want it refused as rta's own state", got, err)
	} else if !strings.Contains(err.Error(), "another name for rta's own state") {
		t.Errorf("refused as %q, want it named as another name for rta's state", err)
	}
	if got, err := Read(req, filepath.Join(moved, "grants.json"), 256); !protectedErr(err) {
		t.Errorf("Read of a file written in it since: %q, %v, want it refused as rta's own state", got, err)
	}
	if d, err := OpenDir(req, moved); !protectedErr(err) {
		if d != nil {
			_ = d.Close()
		}
		t.Errorf("OpenDir of the moved directory: %v, want it refused as rta's own state", err)
	}
	top, err := OpenDir(req, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = top.Close() }()
	info, err := top.Lstat("backup")
	if err != nil {
		t.Fatal(err)
	}
	if top.Withheld("backup", info) == nil {
		t.Error("a walk of the root would list the moved directory as its own")
	}
}
