package pathin

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// OpenDir opens the directory path names, for a walk (Dir).
//
// Under bounds, from the root it lies under, through nothing that changed
// since it was judged. Without, os.OpenRoot on the path as given, which
// follows a link in it as os.ReadDir did: a person at a terminal who names a
// link to a directory means the directory.
func OpenDir(req plugin.Request, path string) (*Dir, error) {
	b := req.Bounds()
	if b.Root == nil {
		root, err := os.OpenRoot(path)
		if err != nil {
			return nil, err
		}
		return &Dir{root: root, path: path}, nil
	}
	d, name, err := bounded(b, path)
	if err != nil || name == "" {
		return d, err
	}
	defer func() { _ = d.Close() }()
	return d.OpenDir(name)
}

// Stat describes what path names, following links as os.Stat does — under
// bounds, only the links the guard followed to judge it, and a link where
// there was none when it did is refused as changed.
func Stat(req plugin.Request, path string) (fs.FileInfo, error) {
	b := req.Bounds()
	if b.Root == nil {
		return os.Stat(path)
	}
	d, name, err := bounded(b, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = d.Close() }()
	if name == "" {
		return d.Stat()
	}
	info, _, err := d.look("stat", name)
	return info, err
}

// Readlink reads what the link path names holds, as os.Readlink does.
//
// Under bounds, never: the guard judged the path by following its links, and
// the handler holds what it judged, so the name is not a link unless one was
// put there since — and what that one holds is a name the caller chose to
// have read back, which may be one outside the roots. What a link the caller
// named held is told another way (plugin.Request.Link).
func Readlink(req plugin.Request, path string) (string, error) {
	b := req.Bounds()
	if b.Root == nil {
		return os.Readlink(path)
	}
	d, name, err := bounded(b, path)
	if err != nil {
		return "", err
	}
	defer func() { _ = d.Close() }()
	if name == "" {
		return "", &fs.PathError{Op: "readlink", Path: path, Err: syscall.EINVAL}
	}
	if _, err := d.Readlink(name); err != nil {
		return "", err
	}
	return "", &fs.PathError{Op: "readlink", Path: path, Err: ErrChanged}
}

// bounded opens the directory holding what path names, from the root b puts
// it under, and returns it with path's name in it — "" when path is the root
// itself, which has no directory holding it inside the root.
func bounded(b plugin.Bounds, path string) (*Dir, string, error) {
	root, rel, err := b.Root(path)
	if err != nil {
		return nil, "", err
	}
	d := &Dir{root: root, path: root.Name(), refuse: b.Refuse}
	if rel == "." {
		return d, "", nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		next, err := d.OpenDir(part)
		_ = d.Close()
		if err != nil {
			return nil, "", err
		}
		d = next
	}
	return d, parts[len(parts)-1], nil
}
