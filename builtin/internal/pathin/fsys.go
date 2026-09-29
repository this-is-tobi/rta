package pathin

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// FS is the directory dir, which req's caller named, as an fs.FS whose files
// nobody named — a scan's: the lockfiles an audit finds in the directory it
// was pointed at. What it opens is only ever a regular file, on every
// surface (OpenFile), and Stat and ReadDir open nothing to read, so neither
// the walk nor the look for each name touches a pipe.
//
// A file a scan finds is named by whoever wrote the directory — a cloned
// repository, an unpacked archive — and not by the caller, so the CLI's
// leave to read a pipe it was pointed at does not reach it: a named pipe
// called package-lock.json held open(2) for good where no context reaches,
// at a terminal as much as over MCP.
//
// Under bounds, each name is judged as a path the call reached from dir and
// opened as Open opens one: a link found in the scan is followed where the
// guard allows and refused where it does not, which is outside the roots and
// rta's own state — os.DirFS followed one anywhere. Without, as os.DirFS
// reads a directory, the link it names followed as the person who named the
// directory would follow it.
func FS(req plugin.Request, dir string) fs.FS {
	if req.Bounds().Root == nil {
		return scanFS{dir: dir, base: os.DirFS(dir)}
	}
	return boundedFS{req: req, dir: dir}
}

type scanFS struct {
	dir  string
	base fs.FS
}

func (s scanFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	f, _, err := OpenFile(filepath.Join(s.dir, filepath.FromSlash(name)))
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (s scanFS) Stat(name string) (fs.FileInfo, error) { return fs.Stat(s.base, name) }

func (s scanFS) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(s.base, name) }

type boundedFS struct {
	req plugin.Request
	dir string
}

func (b boundedFS) path(op, name string) (string, error) {
	if !fs.ValidPath(name) {
		return "", &fs.PathError{Op: op, Path: name, Err: fs.ErrInvalid}
	}
	return filepath.Join(b.dir, filepath.FromSlash(name)), nil
}

func (b boundedFS) Open(name string) (fs.File, error) {
	path, err := b.path("open", name)
	if err != nil {
		return nil, err
	}
	f, _, err := Open(b.req, path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (b boundedFS) Stat(name string) (fs.FileInfo, error) {
	path, err := b.path("stat", name)
	if err != nil {
		return nil, err
	}
	return Stat(b.req, path)
}

// ReadDir lists a directory as entries that hold what each one was when it
// was listed, since the directory is closed before the caller looks at them.
func (b boundedFS) ReadDir(name string) ([]fs.DirEntry, error) {
	path, err := b.path("readdir", name)
	if err != nil {
		return nil, err
	}
	d, err := OpenDir(b.req, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir()
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if info, lerr := d.Lstat(e.Name()); lerr == nil {
			out = append(out, fs.FileInfoToDirEntry(info))
		}
	}
	return out, err
}
