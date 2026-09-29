// Package pathin opens what a built-in's path input names, for the
// capabilities that open what their caller pointed them at: cert's PEM file,
// fs.hash's file, net's hosts and resolv.conf, the lockfile or the directory
// of them an audit reads, and the directory fs.tree and fs.usage walk.
//
// Each of them had an os.ReadFile or an os.Open of its own, and between them
// they had the two ways a caller-named path takes a server down. A file of
// any size was read whole — a pack file or a dataset in an MCP root cost the
// server its size about twice over, 5.88 GB resident after two calls on a
// 2 GiB file — and a named pipe was opened blocking: open(2) on a FIFO waits
// for a writer that never comes, no context reaches into a syscall, and every
// such call pinned an OS thread until the Go runtime aborted the process at
// its ten-thousandth. None of them needed either: a PEM bundle, a hosts file
// and a resolv.conf are small regular files, and fs.hash streams.
//
// The package is the sibling of pipein, which reads what was piped to a
// call's standard input, and takes the same line on surfaces: a stream is a
// command line's business. `echo -n hello | rta fs hash /dev/stdin` is how
// the CLI's guide says a Path input takes a pipe, and a person who names a
// FIFO at their own terminal is waiting on a writer they started, with ^C to
// hand. Everywhere else — an agent over MCP, a form in the TUI, whose stdin
// is the agent's request stream or the keyboard — a path names a file, and
// anything else is refused before it is opened.
//
// A file the caller did not name is read as the other surfaces read one, on
// the CLI too (OpenFile, ReadFile): a lockfile found in the directory an
// audit was pointed at, or the .mcp.json a cloned repository keeps. Nobody
// started a writer for a pipe in its place, and at a terminal it held the
// command for good as surely as it held a server.
//
// And under a root, where a call carries its surface's bounds
// (plugin.Bounds), a path is opened from the root and never by name. The
// guard judges a path once, at the boundary, and a caller who can write
// inside the root can swap the file, or a directory above it, for a link out
// before the handler's open follows the name: 6534 of 13883 fs.hash calls
// read the file outside under such a swap, measured. So every open here, of a
// path named or found, walks down from the root's own descriptor, one
// directory at a time, and opens nothing that was not what it looked at a
// moment before (Dir). What is read is what was judged, or nothing.
package pathin

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// NotAFileError is the refusal of a path that names something other than a
// regular file on a surface that reads only files. The caller words the
// view.Error around it, because only it knows what the file was meant to be.
type NotAFileError struct {
	Path string
	Mode fs.FileMode
}

func (e *NotAFileError) Error() string {
	return fmt.Sprintf("%s is %s, not a file", e.Path, Kind(e.Mode))
}

// TooLargeError is the refusal of a file larger than its caller's format ever
// is. The caller words the view.Error around it, as for NotAFileError.
type TooLargeError struct {
	Path string
	Max  int
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("%s is larger than %s", e.Path, format.Bytes(e.Max))
}

// Kind names what a mode says a path is, for a refusal to say what was found
// in place of a file.
func Kind(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "a directory"
	case m&fs.ModeNamedPipe != 0:
		return "a named pipe"
	case m&fs.ModeSocket != 0:
		return "a socket"
	case m&fs.ModeDevice != 0:
		return "a device"
	}
	return "something other than a regular file"
}

// Open opens path, which req's caller named, to read, and says what it
// opened.
//
// Off the CLI, only a regular file: the path is stat'ed before anything
// opens it, so neither a pipe nor a device is ever opened — opening some
// devices does something, a serial port's control lines among them — and
// then opened non-blocking and stat'ed again, so a pipe put in the file's
// place between the two cannot hold the open either. On the CLI it is
// os.Open, whatever the path names, for the reason the package gives.
//
// Under bounds (plugin.Bounds), from the root the path lies under and
// through nothing that changed since it was judged (Dir), and only a
// regular file, as off the CLI.
func Open(req plugin.Request, path string) (*os.File, fs.FileInfo, error) {
	if b := req.Bounds(); b.Root != nil {
		d, name, err := bounded(b, path)
		if err != nil {
			return nil, nil, err
		}
		defer func() { _ = d.Close() }()
		if name == "" {
			return nil, nil, &NotAFileError{Path: path, Mode: fs.ModeDir}
		}
		return d.OpenFile(name)
	}
	if req.Surface() != plugin.SurfaceCLI {
		return OpenFile(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

// OpenFile opens path to read only if it is a regular file, on every
// surface: Open's line off the CLI, for a file the caller did not name.
func OpenFile(path string) (*os.File, fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, &NotAFileError{Path: path, Mode: info.Mode()}
	}
	// O_NONBLOCK changes nothing about reading a regular file, and is what
	// keeps a FIFO swapped in after the Stat from blocking the open. Windows
	// defines the flag and ignores it, and has no FIFO to open there.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	if info, err = f.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		if err != nil {
			return nil, nil, err
		}
		return nil, nil, &NotAFileError{Path: path, Mode: info.Mode()}
	}
	return f, info, nil
}

// Read reads the whole of the file path names, as Open opens it, refusing
// one over max bytes with a *TooLargeError.
//
// One byte past max is read and refused on, rather than a size checked first,
// for the reason atomicfile.ReadCapped gives: a stat is not the read, and a
// file can grow between the two. The buffer grows with what is read, since a
// cap sized for the largest file somebody keeps is far above the usual one.
func Read(req plugin.Request, path string, max int) ([]byte, error) {
	f, _, err := Open(req, path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadAll(f, path, max)
}

// ReadFile is Read for a file the caller did not name, opened as OpenFile
// opens it.
func ReadFile(path string, max int) ([]byte, error) {
	f, _, err := OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return ReadAll(f, path, max)
}

// ReadAll reads r to its end as Read does, refusing past max bytes with a
// *TooLargeError naming path: for a file opened some other way than from a
// path on this machine, the one in an in-memory clone among them.
func ReadAll(r io.Reader, path string, max int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, &TooLargeError{Path: path, Max: max}
	}
	return data, nil
}
