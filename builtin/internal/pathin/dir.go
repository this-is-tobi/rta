package pathin

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// ErrChanged is the refusal of something that is not what it was a moment
// ago: a name that is a symbolic link where there was none when the path was
// judged, or a directory or file that is not the one looked at before it was
// opened. Under a root that is what a swap looks like, and the answer is the
// same whatever the swap pointed at.
var ErrChanged = errors.New("changed while it was being opened")

// WithheldError is a walk's refusal to enter or open something under a root
// that the call's bounds withhold (plugin.Bounds.Refuse): rta's own state.
type WithheldError struct {
	Path string
	Err  error
}

func (e *WithheldError) Error() string { return e.Err.Error() }
func (e *WithheldError) Unwrap() error { return e.Err }

// Dir is a directory opened for reading, through which what is in it is
// reached by name, from the directory itself, and nothing is followed.
//
// A walk that goes by path names gives every step to whoever can rename
// things: fs.tree looked at an entry, saw a directory, and listed whatever
// was at that name by the time it read it, which a caller who can write in
// the tree could make a link to anywhere. A Dir holds the directory open
// (os.Root), so each entry is looked at and opened relative to it. And each
// open is checked to be what was looked at — os.SameFile on what Lstat saw
// and what was opened — because os.Root does follow a link that stays inside
// the directory it was opened on, which for a walk is one it never meant to
// take.
type Dir struct {
	root   *os.Root
	path   string
	refuse func(string, fs.FileInfo) error
}

// Path is where the directory is, for a message to name and a link's target
// to be told against (plugin.Request.LinkTarget).
func (d *Dir) Path() string { return d.path }

// Close closes the directory.
func (d *Dir) Close() error { return d.root.Close() }

// Stat describes the directory itself.
func (d *Dir) Stat() (fs.FileInfo, error) {
	info, err := d.root.Stat(".")
	return info, d.at("stat", ".", err)
}

// ReadDir lists the directory, sorted by name as os.ReadDir sorts it. An
// entry's Info looks at it through the directory (Lstat): os.File's own
// looks it up by path name, which is the thing a Dir exists not to do.
func (d *Dir) ReadDir() ([]fs.DirEntry, error) {
	f, err := d.root.Open(".")
	if err != nil {
		return nil, d.at("readdir", ".", err)
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	err = d.at("readdir", ".", err)
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	out := make([]fs.DirEntry, len(entries))
	for i, e := range entries {
		out[i] = entry{DirEntry: e, dir: d}
	}
	return out, err
}

type entry struct {
	fs.DirEntry
	dir *Dir
}

func (e entry) Info() (fs.FileInfo, error) { return e.dir.Lstat(e.Name()) }

// Lstat describes name in the directory, a link as itself.
func (d *Dir) Lstat(name string) (fs.FileInfo, error) {
	if err := d.element("lstat", name); err != nil {
		return nil, err
	}
	info, err := d.root.Lstat(name)
	return info, d.at("lstat", name, err)
}

// Readlink reads what the link name holds.
func (d *Dir) Readlink(name string) (string, error) {
	if err := d.element("readlink", name); err != nil {
		return "", err
	}
	target, err := d.root.Readlink(name)
	return target, d.at("readlink", name, err)
}

// OpenDir opens the directory name in this one, refusing a link, and
// anything else put in its place after it was looked at.
func (d *Dir) OpenDir(name string) (*Dir, error) {
	info, full, err := d.look("opendir", name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, &fs.PathError{Op: "opendir", Path: full, Err: syscall.ENOTDIR}
	}
	sub, err := d.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if opened, err := sub.Stat("."); err != nil || !os.SameFile(info, opened) {
		_ = sub.Close()
		return nil, &fs.PathError{Op: "opendir", Path: full, Err: ErrChanged}
	}
	return &Dir{root: sub, path: full, refuse: d.refuse}, nil
}

// OpenFile opens name in this directory to read, only if it is a regular
// file: OpenFile's line (the package's), for the same reasons, and a link or
// anything else put in its place after it was looked at is refused as well.
func (d *Dir) OpenFile(name string) (*os.File, fs.FileInfo, error) {
	info, full, err := d.look("open", name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, &NotAFileError{Path: full, Mode: info.Mode()}
	}
	f, err := d.root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, d.at("open", name, err)
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, nil, &fs.PathError{Op: "open", Path: full, Err: ErrChanged}
	}
	return f, opened, nil
}

// look is what OpenDir and OpenFile see before they open name: what is
// there, as itself, refused when it is a link — nothing a walk opens is
// followed — or when the bounds withhold it.
func (d *Dir) look(op, name string) (fs.FileInfo, string, error) {
	if err := d.element(op, name); err != nil {
		return nil, "", err
	}
	full := filepath.Join(d.path, name)
	info, err := d.root.Lstat(name)
	if err != nil {
		return nil, "", d.at(op, name, err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, "", &fs.PathError{Op: op, Path: full, Err: ErrChanged}
	}
	if d.refuse != nil {
		if err := d.refuse(full, info); err != nil {
			return nil, "", &WithheldError{Path: full, Err: err}
		}
	}
	return info, full, nil
}

// Withheld reports why the bounds withhold name, which info describes, as a
// *WithheldError, or nil. For the walk that lists what it does not open: a
// file of rta's own configuration that lies under a root — RTA_CONFIG or the
// ./.rta.yaml beside a project, and the remotes.yaml next to it — is refused
// by name and was listed with its size, where the directory holding the
// data was listed as withheld.
func (d *Dir) Withheld(name string, info fs.FileInfo) error {
	if d.refuse == nil {
		return nil
	}
	full := filepath.Join(d.path, name)
	if err := d.refuse(full, info); err != nil {
		return &WithheldError{Path: full, Err: err}
	}
	return nil
}

// at is err, from os.Root, as the path it was about is named everywhere
// else: by the operation a caller asked for and the whole path. os.Root names
// the system call it made and the name in the directory, so a missing file
// read "reading /srv/etc/hosts: statat hosts: no such file or directory" where
// an open by name said "stat /srv/etc/hosts: …".
func (d *Dir) at(op, name string, err error) error {
	var pe *fs.PathError
	if !errors.As(err, &pe) {
		return err
	}
	return &fs.PathError{Op: op, Path: filepath.Join(d.path, name), Err: pe.Err}
}

// element refuses a name that is not one entry of this directory: a Dir
// walks one step at a time, and a name with a separator in it, or "..",
// would be os.Root's walk instead, which follows links.
func (d *Dir) element(op, name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/`+string(filepath.Separator)) {
		return &fs.PathError{Op: op, Path: filepath.Join(d.path, name), Err: fs.ErrInvalid}
	}
	return nil
}
