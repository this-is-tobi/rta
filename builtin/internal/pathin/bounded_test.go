package pathin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// under is a request bounded to root, as the MCP bridge hands one to a
// handler.
func under(t *testing.T, root string) plugin.Request {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return on(plugin.SurfaceMCP).WithBounds(g.Bounds())
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// Under bounds a path opens from its root, and one a link leads out of is
// refused however it is reached: named, or found by a scan.
func TestBoundedOpensOnlyUnderTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	put(t, filepath.Join(root, "in.txt"), "inside")
	put(t, filepath.Join(outside, "secret"), "outside")
	link(t, filepath.Join(outside, "secret"), filepath.Join(root, "out"))
	link(t, "in.txt", filepath.Join(root, "alias"))
	req := under(t, root)

	if got, err := Read(req, filepath.Join(root, "in.txt"), 64); err != nil || string(got) != "inside" {
		t.Errorf("a file inside: %q, %v", got, err)
	}
	if got, err := Read(req, filepath.Join(root, "alias"), 64); err != nil || string(got) != "inside" {
		t.Errorf("a link to a file inside: %q, %v", got, err)
	}
	if got, err := Read(req, filepath.Join(root, "out"), 64); err == nil {
		t.Errorf("a link out was read: %q", got)
	}
	if _, err := Stat(req, filepath.Join(root, "out")); err == nil {
		t.Error("a link out was described")
	}
	scan := FS(req, root)
	if _, err := fs.Stat(scan, "out"); err == nil {
		t.Error("a scan described a link out")
	}
	if got, err := fs.ReadFile(scan, "alias"); err != nil || string(got) != "inside" {
		t.Errorf("a scan's link to a file inside: %q, %v", got, err)
	}
	if _, err := fs.ReadFile(scan, "out"); err == nil {
		t.Error("a scan read a link out")
	}
}

// The root itself is a directory, which Open refuses as it refuses any, and
// which OpenDir opens.
func TestBoundedRootIsADirectory(t *testing.T) {
	root := t.TempDir()
	req := under(t, root)
	var notAFile *NotAFileError
	if _, _, err := Open(req, root); !errors.As(err, &notAFile) {
		t.Errorf("Open(root) = %v, want a NotAFileError", err)
	}
	d, err := OpenDir(req, root)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
}

// An error under bounds names what was asked and the path whole, as an open
// by name does. os.Root's own named the system call it made and the name in
// the directory: "statat hosts", in a message about /srv/etc/hosts.
func TestABoundedErrorNamesWhatWasAskedAndThePathWhole(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "etc", "other"), "x")
	req := under(t, root)
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(resolved, "etc", "hosts")
	_, readErr := Read(req, filepath.Join(root, "etc", "hosts"), 64)
	_, statErr := Stat(req, filepath.Join(root, "etc", "hosts"))
	for op, err := range map[string]error{"open": readErr, "stat": statErr} {
		var pe *fs.PathError
		if !errors.As(err, &pe) || pe.Op != op || pe.Path != want || !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s of a missing file: %v, want %q naming %s", op, err, op, want)
		}
	}
}

// Nothing a Dir opens is followed: a link where a walk looks is refused as
// changed, since a walk never takes one, and a name that is not one entry is
// refused before anything is asked.
func TestADirFollowsNothing(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "sub", "f"), "x")
	link(t, "sub", filepath.Join(root, "dirlink"))
	link(t, filepath.Join("sub", "f"), filepath.Join(root, "filelink"))
	d, err := OpenDir(on(plugin.SurfaceCLI), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	if _, err := d.OpenDir("dirlink"); !errors.Is(err, ErrChanged) {
		t.Errorf("OpenDir(link) = %v, want ErrChanged", err)
	}
	if _, _, err := d.OpenFile("filelink"); !errors.Is(err, ErrChanged) {
		t.Errorf("OpenFile(link) = %v, want ErrChanged", err)
	}
	for _, name := range []string{"..", ".", "", "sub/f"} {
		if _, _, err := d.OpenFile(name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("OpenFile(%q) = %v, want ErrInvalid", name, err)
		}
	}
	sub, err := d.OpenDir("sub")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	if f, _, err := sub.OpenFile("f"); err != nil {
		t.Errorf("a file in a directory: %v", err)
	} else {
		_ = f.Close()
	}
}

// A walk under a root that reaches rta's own state, which lies inside it
// when the root is the home directory, is refused there as a path naming it
// is: nothing in it is listed or opened.
func TestAWalkIsRefusedRtasOwnState(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, ".local", "share", "rta")
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	put(t, filepath.Join(data, "grants.key"), "seal")
	req := under(t, root)
	share, err := OpenDir(req, filepath.Join(root, ".local", "share"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = share.Close() }()
	var withheld *WithheldError
	if _, err := share.OpenDir("rta"); !errors.As(err, &withheld) {
		t.Errorf("a walk into the data directory: %v, want a WithheldError", err)
	}
	if _, err := OpenDir(req, data); err == nil {
		t.Error("the data directory named was opened")
	}
}

// The handler holds a path the guard judged, and a link put in its place, or
// in the place of a directory above it, before the handler opens it must
// lead nowhere a caller can tell apart: every way of opening refuses it in
// the same words whether what it points at outside exists or not.
func TestALinkPutInPlaceAnswersTheSameWhetherItsTargetExists(t *testing.T) {
	for _, swapped := range []string{"the file", "a directory above it"} {
		t.Run(swapped, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			file := filepath.Join(root, "etc", "hosts")
			put(t, file, "inside")
			g, err := pathguard.New(root)
			if err != nil {
				t.Fatal(err)
			}
			judged, verr := g.Check("path", file)
			if verr != nil {
				t.Fatal(verr)
			}
			req := on(plugin.SurfaceMCP).WithBounds(g.Bounds())
			at, target := file, filepath.Join(outside, "hosts")
			if swapped != "the file" {
				at, target = filepath.Dir(file), outside
			}
			if err := os.RemoveAll(at); err != nil {
				t.Fatal(err)
			}
			link(t, target, at)
			answers := func() []string {
				_, _, openErr := Open(req, judged)
				_, statErr := Stat(req, judged)
				_, linkErr := Readlink(req, judged)
				_, dirErr := OpenDir(req, judged)
				var out []string
				for _, err := range []error{openErr, statErr, linkErr, dirErr} {
					if err == nil {
						t.Fatal("a link out was opened")
					}
					out = append(out, err.Error())
				}
				return out
			}
			put(t, filepath.Join(outside, "hosts"), "outside")
			there := answers()
			if err := os.Remove(filepath.Join(outside, "hosts")); err != nil {
				t.Fatal(err)
			}
			gone := answers()
			for i := range there {
				if there[i] != gone[i] {
					t.Errorf("the answer tells whether the target exists:\n  present: %s\n  missing: %s", there[i], gone[i])
				}
			}
		})
	}
}

// On a volume that ignores case, a path spelled in another case than its
// root is under it, as the guard judges by identity, and it opens from the
// root by where it lies there: a walk names what it finds under the root's
// own spelling, which is what a link's target is told against.
func TestABoundedPathSpelledInAnotherCaseOpensFromItsRoot(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	put(t, filepath.Join(root, "sub", "in.txt"), "inside")
	upper := filepath.Join(base, "PROJ")
	if _, err := os.Stat(upper); err != nil {
		t.Skip("this volume tells case apart")
	}
	req := under(t, root)
	if got, err := Read(req, filepath.Join(upper, "sub", "in.txt"), 64); err != nil || string(got) != "inside" {
		t.Fatalf("a case variant under the root: %q, %v", got, err)
	}
	d, err := OpenDir(req, filepath.Join(upper, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close() }()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolved, "sub"); d.Path() != want {
		t.Errorf("Path() = %q, want it under the root's spelling, %q", d.Path(), want)
	}
}
