package git

import (
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// boundDir is a directory one call reads in: over MCP through an *os.Root, so
// that nothing read through it leaves the directory, and anywhere else by
// name, as git reads.
//
// **A path the gate judged is a name, and what a name leads to can change
// before it is opened.** Every path this package derives — the repository a
// walk found, the git directory a .git file names, its common directory, the
// hooks directory, core.excludesFile, a file an include names — is put to the
// host (plugin.Request.Confine) and then opened by name: go-git through
// go-billy's osfs, this package's own reads through the os package. A caller
// who can write inside the roots, with its own file tools, can swap a
// directory or a file on that path for a link out of them after the
// judgement and before the open, and the open followed it: a git directory
// swapped for a link to another repository's the moment the gate had judged
// it answered git.log, git.diff and git.config from that repository.
// go-billy's chroot resolves the links under its directory itself, and
// refuses one leading out, but opens by name after, and a link swapped in
// between is followed as well; the directory itself it resolves once, when
// it is made, and never again. Its bound mode, osfs.WithBoundOS, is no
// better: it resolves a name in the process with securejoin and opens the
// name it reached, which a swap in between leads out of again, and it reads
// an absolute link inside the directory as though the directory were the top
// of the filesystem, which is not where git reads it.
//
// An *os.Root opens each part of a name from the directory the part before
// it opened, refusing a link that leads out of the directory the root was
// opened on whatever the link says by the time it is read, and holds that
// directory open: a swap of the directory itself, once it is opened, changes
// nothing read through it. The root is opened from the directory of the
// server's roots the path lies under (rootAbove), which a caller cannot swap,
// and the directory from that, so the opening is bounded as well.
type boundDir struct {
	// path is the directory as it was judged, which messages name it by.
	path string
	// root reads it, nil where it is read by name.
	root *os.Root
}

// bounded reports whether req's reads are bounded to the roots: over MCP,
// where the host confines the paths a call reaches. A terminal reads as git
// reads, by name, wherever a link leads.
func bounded(req plugin.Request) bool { return req.Surface() == plugin.SurfaceMCP }

// inRoot is name as an *os.Root takes it: relative to its directory, "." for
// the directory itself. go-billy hands a name relative to the directory it
// was opened on, and may spell it with a leading separator.
func inRoot(name string) string {
	name = strings.TrimLeft(filepath.ToSlash(name), "/")
	if name == "" {
		return "."
	}
	return name
}

// join is name, as go-billy and a working tree's paths spell it, with a
// forward slash, under d.
func (d boundDir) join(name string) string { return filepath.Join(d.path, filepath.FromSlash(name)) }

func (d boundDir) Stat(name string) (os.FileInfo, error) {
	if d.root == nil {
		return os.Stat(d.join(name))
	}
	return d.root.Stat(inRoot(name))
}

func (d boundDir) Lstat(name string) (os.FileInfo, error) {
	if d.root == nil {
		return os.Lstat(d.join(name))
	}
	return d.root.Lstat(inRoot(name))
}

func (d boundDir) Readlink(name string) (string, error) {
	if d.root == nil {
		return os.Readlink(d.join(name))
	}
	return d.root.Readlink(inRoot(name))
}

// OpenFile opens name with flag, which is only ever a read here.
func (d boundDir) OpenFile(name string, flag int) (*os.File, error) {
	if d.root == nil {
		return os.OpenFile(d.join(name), flag, 0)
	}
	return d.root.OpenFile(inRoot(name), flag, 0)
}

// ReadDir is the entries of the directory name, sorted by name, as
// os.ReadDir gives them.
//
// Opened without waiting, as every file here is. os.ReadDir opens a
// directory as one, which a named pipe refuses at once, and an *os.Root
// opens a name as whatever is there: a pipe where a directory was named — a
// core.hooksPath naming one — held open(2) until a writer came, which no
// context can interrupt. It is refused as not a directory instead. Each
// entry's own information is read from the directory the listing opened, not
// by name, where it is read through a root.
func (d boundDir) ReadDir(name string) ([]os.DirEntry, error) {
	f, err := d.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	entries, err := f.ReadDir(-1)
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, err
}

// sub is the directory name under d, read as d is. A root's is opened from
// d's, and closed by whoever closes it.
func (d boundDir) sub(name string) (boundDir, error) {
	if d.root == nil {
		return boundDir{path: d.join(name)}, nil
	}
	root, err := d.root.OpenRoot(onlyADirectory(inRoot(name), "/"))
	if err != nil {
		return boundDir{}, err
	}
	return boundDir{path: d.join(name), root: root}, nil
}

// Close lets go of the directory a root holds open.
func (d boundDir) Close() {
	if d.root != nil {
		_ = d.root.Close()
	}
}

// onlyADirectory is name, a directory to open as a root, spelled so that it
// opens only if it is one: with the directory itself, ".", after it, sep
// between them.
//
// **os.OpenRoot and Root.OpenRoot open the last part of a name as whatever
// is there**, and a named pipe waits in open(2) for a writer that never
// comes, which no context can interrupt: a .git file naming a pipe as its
// git directory, or a directory swapped for one as it was opened, held the
// call for good. Every part of a name but the last is opened as a directory,
// which a pipe refuses at once, and "." after it makes it one of those.
func onlyADirectory(name, sep string) string { return name + sep + "." }

// rootAbove is the directory of the server's roots p lies under: the first
// of the directories on p's way, from the top of the filesystem down, that
// the gate holds in bounds, p itself where that is the first. Where there is
// none, verr is the gate's refusal of p.
//
// **From the top down, and not up from p.** Walking up, the last directory the
// gate holds before it refuses one is the root only while nothing on the way
// changes: a directory under the root that a caller swaps for a link out as
// the walk passes it is refused, and ends the walk below it, at a directory
// the caller can swap again once it is opened by name. The directories above
// a root are no caller's to write, so the first one the gate holds, coming
// down, is the root itself, whatever is done under it meanwhile.
//
// p is taken a part at a time as it is spelled, not cleaned: the gate resolves
// a .. after a link as the kernel does, where cleaning would take the .. off
// the name first.
func rootAbove(req plugin.Request, p string) (_ string, verr *view.Error) {
	sep := string(filepath.Separator)
	vol := filepath.VolumeName(p)
	cur := vol + sep
	var parts []string
	for _, part := range strings.Split(p[len(vol):], sep) {
		if part != "" {
			parts = append(parts, part)
		}
	}
	for i := 0; ; i++ {
		if _, verr = req.Confine("path", cur); verr == nil {
			return cur, nil
		}
		if i == len(parts) {
			return "", verr
		}
		cur = strings.TrimSuffix(cur, sep) + sep + parts[i]
	}
}

// beneathRoots looks at paths the gate judged for one call, each from the
// directory of the server's roots it lies under (rootAbove), opened once for
// the call and closed when it is done, as the call's reads are bounded
// (bounded); by name where they are not.
//
// From the root, and not from the directory each path is in: a walk that
// looks for a repository (repoRoot) asks what git's discovery asks, which
// follows a link at .git wherever it leads, and reading it from a root held
// open at the directory would refuse a link to a sibling that git follows,
// and send the walk on up to a repository above it.
type beneathRoots struct {
	req    plugin.Request
	opened []boundDir
}

// under is the directory p is looked at from, and p's name in it.
//
// A root drawn around a file alone leaves no directory to open it from, and
// the name is no caller's to swap, the directory holding it being outside the
// roots: it is looked at by name.
func (b *beneathRoots) under(p string) (boundDir, string, error) {
	if !bounded(b.req) {
		return boundDir{}, p, nil
	}
	for _, d := range b.opened {
		if rel, err := filepath.Rel(d.path, p); err == nil && !climbsOut(rel) {
			return d, rel, nil
		}
	}
	top, verr := rootAbove(b.req, p)
	if verr != nil {
		return boundDir{}, "", verr
	}
	root, err := os.OpenRoot(onlyADirectory(top, string(filepath.Separator)))
	if errors.Is(err, syscall.ENOTDIR) && top == p {
		return boundDir{}, p, nil
	}
	if err != nil {
		return boundDir{}, "", err
	}
	d := boundDir{path: top, root: root}
	b.opened = append(b.opened, d)
	rel, err := filepath.Rel(top, p)
	return d, rel, err
}

func (b *beneathRoots) Stat(p string) (os.FileInfo, error) {
	d, name, err := b.under(p)
	if err != nil {
		return nil, err
	}
	return d.Stat(name)
}

func (b *beneathRoots) Lstat(p string) (os.FileInfo, error) {
	d, name, err := b.under(p)
	if err != nil {
		return nil, err
	}
	return d.Lstat(name)
}

// dir opens the directory p, held open where it is read through a root, and
// closed by the caller.
func (b *beneathRoots) dir(p string) (boundDir, error) {
	d, name, err := b.under(p)
	if err != nil {
		return boundDir{}, err
	}
	sub, err := d.sub(name)
	sub.path = p
	return sub, err
}

// close lets go of every directory the call opened.
func (b *beneathRoots) close() {
	for _, d := range b.opened {
		d.Close()
	}
	b.opened = nil
}

// openBoundDir opens judged, a directory the gate put back for req, as req's
// reads are bounded (bounded): beneath the root it lies under, over MCP, and
// by name anywhere else. The caller closes it.
func openBoundDir(req plugin.Request, judged string) (boundDir, error) {
	b := beneathRoots{req: req}
	defer b.close()
	return b.dir(judged)
}

// repoFiles opens the directories one call reads a repository from (openAt),
// as the call's reads are bounded (bounded), and closes those it holds open
// when the call is done.
type repoFiles struct {
	req    plugin.Request
	mu     sync.Mutex
	opened []boundDir
}

// at is judged, a directory of the repository the gate put back, as go-git
// reads it: through an *os.Root over MCP (rootFiles), and go-billy's osfs
// anywhere else, as it always was.
func (o *repoFiles) at(judged string) (billy.Filesystem, error) {
	if !bounded(o.req) {
		return osfs.New(judged), nil
	}
	d, err := openBoundDir(o.req, judged)
	if err != nil {
		return nil, err
	}
	o.keep(d)
	return rootFiles{dir: d, files: o}, nil
}

func (o *repoFiles) keep(d boundDir) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opened = append(o.opened, d)
}

// close lets go of every directory the call opened.
func (o *repoFiles) close() {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, d := range o.opened {
		d.Close()
	}
	o.opened = nil
}

// rootFiles is a go-billy filesystem over a boundDir read through an
// *os.Root: what go-git reads a repository through over MCP, in place of
// osfs. It writes nothing, as regularFiles, which it is read through, writes
// nothing either.
type rootFiles struct {
	dir   boundDir
	files *repoFiles
}

func (f rootFiles) Open(name string) (billy.File, error) { return f.OpenFile(name, os.O_RDONLY, 0) }

func (f rootFiles) OpenFile(name string, flag int, _ os.FileMode) (billy.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, &iofs.PathError{Op: "open", Path: name, Err: errReadOnly}
	}
	file, err := f.dir.OpenFile(name, flag)
	if err != nil {
		return nil, err
	}
	return rootFile{File: file, name: name}, nil
}

func (f rootFiles) Stat(name string) (os.FileInfo, error)  { return f.dir.Stat(name) }
func (f rootFiles) Lstat(name string) (os.FileInfo, error) { return f.dir.Lstat(name) }
func (f rootFiles) Readlink(name string) (string, error)   { return f.dir.Readlink(name) }
func (f rootFiles) Join(elem ...string) string             { return filepath.Join(elem...) }
func (f rootFiles) Root() string                           { return f.dir.path }

// ReadDir is what go-billy's osfs gives for a listing: each entry's
// information, sorted by name.
func (f rootFiles) ReadDir(name string) ([]os.FileInfo, error) {
	entries, err := f.dir.ReadDir(name)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// Chroot is the directory name under f, opened from f's own: a git
// directory's modules/ holds a submodule's repository, which go-git opens
// this way.
func (f rootFiles) Chroot(name string) (billy.Filesystem, error) {
	d, err := f.dir.sub(name)
	if err != nil {
		return nil, err
	}
	f.files.keep(d)
	return rootFiles{dir: d, files: f.files}, nil
}

func (f rootFiles) Create(name string) (billy.File, error) {
	return nil, &iofs.PathError{Op: "create", Path: name, Err: errReadOnly}
}

func (f rootFiles) TempFile(dir, _ string) (billy.File, error) {
	return nil, &iofs.PathError{Op: "createtemp", Path: dir, Err: errReadOnly}
}

func (f rootFiles) Rename(from, _ string) error {
	return &iofs.PathError{Op: "rename", Path: from, Err: errReadOnly}
}

func (f rootFiles) Remove(name string) error {
	return &iofs.PathError{Op: "remove", Path: name, Err: errReadOnly}
}

func (f rootFiles) MkdirAll(name string, _ os.FileMode) error {
	return &iofs.PathError{Op: "mkdir", Path: name, Err: errReadOnly}
}

func (f rootFiles) Symlink(_, link string) error {
	return &iofs.PathError{Op: "symlink", Path: link, Err: errReadOnly}
}

// rootFile is a file rootFiles opened, named as go-billy's own files are
// named, by the name it was opened by. Nothing here writes, and the lock git
// takes only to write is refused with it.
type rootFile struct {
	*os.File
	name string
}

func (f rootFile) Name() string  { return f.name }
func (f rootFile) Lock() error   { return &iofs.PathError{Op: "lock", Path: f.name, Err: errReadOnly} }
func (f rootFile) Unlock() error { return nil }

// worktreeDir is the directory the working tree wt of a repository openAt
// opened is read from, for this package's own reads of it beside go-git's:
// the directory it is held open by over MCP, and its name anywhere else.
//
// go-git hands a working tree back wrapped in a filesystem of its own, which
// passes on no method of the one it wraps but Chroot: the directory itself,
// asked for through that, is the rootFiles openAt made, opened once more from
// the directory it holds. Over MCP anything else is refused rather than read
// by name, which is the read this exists to replace.
func worktreeDir(req plugin.Request, wt *git.Worktree) (boundDir, error) {
	if !bounded(req) {
		return boundDir{path: wt.Filesystem.Root()}, nil
	}
	fs, err := wt.Filesystem.Chroot(".")
	if err != nil {
		return boundDir{}, err
	}
	for {
		switch f := fs.(type) {
		case regularFiles:
			fs = f.Filesystem
		case rootFiles:
			return f.dir, nil
		default:
			return boundDir{}, fmt.Errorf("the working tree at %s is not read from the directory the gate judged",
				wt.Filesystem.Root())
		}
	}
}
