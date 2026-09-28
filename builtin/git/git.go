// Package git gives an agent a structured, uniform view of a repository's
// state — status, log, diff, branches, blame, config, hooks — without
// parsing porcelain output meant for a terminal.
//
// Deliberately read-only, on purpose and not by omission: this plugin has
// no git.commit, git.push, or git.clone, the same non-goal reasoning
// The same reasoning applied to gh, helm and kubectl — the git CLI already
// owns mutation well, and the differentiator here is a uniform structured
// view an agent can consume, not reimplementing git. Every capability is
// Read: none of it reveals a secret or crosses a trust boundary the way
// kv.get or ssh.exec would, since a repository's history and diffs are not
// credentials.
package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Plugin returns the git plugin declaration.
func Plugin() plugin.Plugin {
	return plugin.Plugin{
		Name:    "git",
		Summary: "Structured views of a git repository — status, log, diff, branches, blame, config, hooks",
		Capabilities: []plugin.Capability{
			overviewCapability(),
			statusCapability(),
			logCapability(),
			diffCapability(),
			branchesCapability(),
			blameCapability(),
			remotesCapability(),
			configCapability(),
			hooksCapability(),
		},
	}
}

// pathField is the repository location every capability here starts from —
// the same shape and default builtin/fs and builtin/audit already use for
// "which directory", so an agent that already knows one plugin's --path
// input knows this one. Unlike fs/audit, it also accepts a remote URL: a
// local checkout is not always what is at hand.
//
// The help says where that stops, because the MCP schema is generated from
// it and a schema that advertises what the handler refuses is a schema that
// lies — see refuseRemoteOverMCP.
func pathField(help string) plugin.Field {
	return plugin.Field{Name: "path", Type: plugin.Path, Positional: true, Default: ".",
		Help: help + " — or, from a terminal, a remote URL (https://, ssh://, " +
			"git@host:path) cloned in memory; not over MCP"}
}

// openRepo opens the repository at (or above) path, or clones it into memory
// if path names a remote instead of a local one. Both halves of "remote" —
// telling one apart from a local path, and who may ask for one — live in
// builtin/internal/gitclone, because builtin/audit asks the same two
// questions about the same URLs.
//
// No shallow clone here: `git log` and `git blame` are the history, and a
// depth of one would answer them with a single commit.
//
// done closes the packfiles reading the repository kept open (keptPacks),
// and every caller defers it.
func openRepo(ctx context.Context, req plugin.Request) (repo *git.Repository, done func(), _ *view.Error) {
	return open(ctx, req, readsObjects)
}

// openRepoConfigOnly is the same repository for the two capabilities whose
// answers come out of its config and its hooks directory alone, git.config
// and git.hooks, and openRepoRefs for git.remotes, which reads its refs as
// well. An object database this reader can only see part of, a partial
// clone's (notPartial) or one in a format it does not read, cannot make any
// of those wrong, and neither can refs it does not read for the first two;
// refusing them would report a fault in an answer that does not have one,
// and left a hooks audit impossible in a sha256 repository
// (repositoryFormat).
//
// Named openers rather than a flag on openRepo, so that a capability which
// grows an object read has to come here and change which one it calls.
func openRepoConfigOnly(ctx context.Context, req plugin.Request) (repo *git.Repository, done func(), _ *view.Error) {
	return open(ctx, req, readsConfig)
}

func openRepoRefs(ctx context.Context, req plugin.Request) (repo *git.Repository, done func(), _ *view.Error) {
	return open(ctx, req, readsRefs)
}

func open(ctx context.Context, req plugin.Request, what reads) (*git.Repository, func(), *view.Error) {
	path := req.String("path")
	if gitclone.IsRemote(path) {
		if verr := gitclone.RefuseOverMCP(req, "repository"); verr != nil {
			return nil, nil, verr
		}
		repo, verr := gitclone.InMemory(ctx, path, gitclone.Options{})
		return repo, func() {}, verr
	}
	// **The repository is a path this handler derives, not one it was given.**
	// DetectDotGit walks upward, which is what an operator standing in a
	// subdirectory means and is an escape for a caller whose reach was bounded
	// at the boundary: a root over a directory that is not itself a checkout,
	// with a repository above it — `~/.git` from an operator who versions
	// their dotfiles is the ordinary case — puts that repository's whole
	// history, config and file contents inside `git.diff`. The argument passed
	// the guard; the thing opened never went past it.
	//
	// So the walk happens here, one level at a time, and each level is put
	// back to the host before it is used. Unconfined surfaces answer yes to
	// all of them and behave exactly as before.
	root, verr := repoRoot(req, path)
	if verr != nil {
		return nil, nil, verr
	}
	repo, verr := openAt(req, root, path, what)
	if verr != nil {
		return nil, nil, verr
	}
	done := func() { release(repo) }
	if what == readsObjects {
		verr := objectsAllReadable(repo, root)
		if verr == nil {
			verr = partialClone(ctx, req, repo, root)
		}
		if verr != nil {
			done()
			return nil, nil, verr
		}
	}
	return repo, done, nil
}

// keptPacks is how many packfiles reading one repository keeps open between
// the objects it reads, which release closes.
//
// **go-git's default storage opens the packfile again for every object it
// reads, and closes it after.** Each read paid an open, the stat regularFiles
// judges it by, a fresh packfile reader and a close, and that was most of the
// CPU of every call that reads many objects: on a clone of open-webui, 135
// thousand objects in one pack, git_log of one file's 500 commits took 55 s
// of CPU, 35 s of it in the kernel, and git_blame of a file with a long
// history ran into its two seconds having traced part of it. Kept open, the
// same log takes 2.8 s and the blame finishes in 0.8 s.
//
// A bound rather than every pack the repository has: the packs are files a
// caller can write, and a descriptor for each of ten thousand would be ten
// thousand the server's other calls cannot open. Fifty is as many as git lets
// a repository gather before `git gc --auto` packs them into one
// (gc.autoPackLimit), so a repository git maintains keeps every pack open,
// and one with more reopens the ones it has let go, as each read did before.
const keptPacks = 50

// release closes the packfiles reading repo kept open. The repository can
// still be read after, and reads each pack again as it needs it.
func release(repo *git.Repository) {
	if store, ok := repo.Storer.(*filesystem.Storage); ok {
		_ = store.Close()
	}
}

// objectsAllReadable refuses a repository whose object database this reader
// can only see part of.
//
// **go-git finds packfiles by their filename and by nothing else.** Its
// ObjectPacks keeps a file only if it is named `pack-<hash>.pack`, and it
// takes the hash from that name rather than from the index inside — see
// storage/filesystem/dotgit. git makes no such promise. `git maintenance run
// --task=loose-objects`, which `git maintenance start` schedules and which a
// great many working repositories have therefore run, writes its pack as
// `loose-<hash>.pack`; every object inside one is simply absent as far as
// this reader is concerned.
//
// Absent, and not an error, which is the whole problem: a subtree that will
// not load reads as a subtree that was never there. On this project's own
// checkout — five packs under the expected name and two under git's
// maintenance name — that turned a clean working tree into `git status`
// reporting three hundred and seventy-five files as newly staged, and it
// would truncate a log or a diff the same quiet way, with nothing anywhere
// saying the answer was partial. Wrong and plausible about the state of
// somebody's repository is the one answer a boundary must not give, so this
// is refused instead, with the single command that fixes it for good.
//
// The condition is exactly go-git's own skip rule, so it cannot report a
// pack that is in fact being read: the prefix, and a name whose middle is
// not a hash (which go-git drops as "badly-formatted" a line further on).
// An in-memory clone has no pack directory and is left alone.
func objectsAllReadable(repo *git.Repository, root string) *view.Error {
	store, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil
	}
	// The same filesystem dotgit reads, so `.git` files, linked worktrees
	// and common directories are already resolved rather than re-derived.
	entries, err := store.Filesystem().ReadDir(filepath.Join("objects", "pack"))
	if err != nil {
		return nil //nolint:nilerr // no pack directory is no skipped pack — a repository with nothing packed yet, and not a fault to report
	}
	var skipped []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".pack") {
			continue
		}
		if stem, found := strings.CutPrefix(name, "pack-"); found &&
			!plumbing.NewHash(strings.TrimSuffix(stem, ".pack")).IsZero() {
			continue
		}
		skipped = append(skipped, name)
	}
	if len(skipped) == 0 {
		return nil
	}
	return view.Errorf("git.objects.unreadable",
		"%s holds %s this reader will not open: %s",
		root, format.CountOf(len(skipped), "packfile"), strings.Join(skipped, ", ")).
		WithHint("the objects in them read as missing rather than as an error, which is how a clean " +
			"checkout comes back as hundreds of staged files — `git repack -ad` rewrites every pack " +
			"under the name this reads, and the answers here are right again")
}

// openAt opens the repository at root, an exact path repoRoot found: a
// checkout's root, whose .git it opens, or a bare repository, which is its
// own git directory. It is git.PlainOpenWithOptions with the common
// directory enabled, taken apart for two reasons, each of which the library's
// own opener leaves no place for.
//
// **A checkout's root names its repository; it does not have to hold it.**
// A `.git` that is a file says `gitdir: <anywhere>` — how a linked worktree
// and a submodule find theirs — and go-git opens whatever it names. A git
// directory holding a `commondir` file keeps its objects, refs and config in
// the directory that file names, opened the same way. Each is a pointer
// written inside the root to somewhere outside it, and a root is somewhere a
// caller can write — with its own file tools, by unpacking an archive, by
// vendoring a tree. `sub/.git` reading `gitdir: /home/you/other/.git` handed
// git.log, git.diff --commit and git.config the whole of a repository the
// root was drawn to exclude, while fs.tree on that same directory was
// refused. repoRoot judged the directory holding `.git`, and the directory
// read was never it. So each directory is put back to the host as it is
// found, the way repoRoot puts back each level of its walk, and before
// anything in it is read: go-git's opener read HEAD and the config of
// whatever the pointer named before anything here could judge it. A symlink
// inside either directory needs nothing more — go-billy's chroot refuses to
// follow a link out of the directory it was opened on, so `.git/objects`
// linked elsewhere fails as a crossed boundary on every surface.
//
// **And nothing in a repository is opened that is not a file.** go-git opened
// the `.git` file, HEAD, the config and the index as it found them, and
// open(2) on a named pipe with no writer blocks until one comes, which no
// context can interrupt: every capability here, on a repository holding one,
// never answered, and each call held an OS thread for good. regularFiles is
// the filesystem both the git directory and the working tree are read
// through.
//
// And its format is decided as git decides it, for what the capability
// reads, before go-git is handed it (repositoryFormat).
func openAt(req plugin.Request, root, path string, what reads) (*git.Repository, *view.Error) {
	notARepo := func(err error) *view.Error {
		return view.Errorf("git.notarepo", "%s is not a git repository: %v", path, err).
			WithHint("run this against a directory inside a git repository, a checkout's own root, or a bare repository's own directory")
	}
	var wt billy.Filesystem = regularFiles{Filesystem: osfs.New(root)}
	var dot billy.Filesystem = regularFiles{Filesystem: osfs.New(root), gitDir: true}
	info, err := wt.Stat(gitDirName)
	switch {
	case err == nil && info.IsDir():
		if dot, err = dot.Chroot(gitDirName); err != nil {
			return nil, notARepo(err)
		}
	case err == nil:
		gitDir, err := gitDirPointer(wt)
		if err != nil {
			return nil, notARepo(err)
		}
		dot = regularFiles{Filesystem: osfs.New(against(root, gitDir)), gitDir: true}
	case errors.Is(err, iofs.ErrNotExist):
		wt = nil
	default:
		return nil, notARepo(err)
	}
	if _, verr := req.Confine("path", dot.Root()); verr != nil {
		return nil, verr
	}
	if _, err := dot.Stat(""); err != nil {
		return nil, notARepo(err)
	}

	// An untyped nil where there is no common directory: that is how dotgit
	// is told there is none.
	var common billy.Filesystem
	objects := dot
	named, err := readPointer(dot, "commondir")
	if err != nil {
		return nil, notARepo(err)
	}
	if named = strings.TrimSpace(named); named != "" {
		dir := against(dot.Root(), named)
		if _, verr := req.Confine("path", dir); verr != nil {
			return nil, verr
		}
		common = regularFiles{Filesystem: osfs.New(dir), gitDir: true}
		if _, err := common.Stat(""); err != nil {
			return nil, notARepo(git.ErrRepositoryIncomplete)
		}
		objects = common
	}
	if verr := alternatesInBounds(req, objects); verr != nil {
		return nil, verr
	}
	if verr := wholeFilesInBounds(dot, objects, wt); verr != nil {
		return nil, verr
	}

	storage := filesystem.NewStorageWithOptions(dotgit.NewRepositoryFilesystem(dot, common), cache.NewObjectLRUDefault(),
		filesystem.Options{MaxOpenDescriptors: keptPacks})
	// A config that cannot be read is go-git's to refuse, as it refused it.
	if cfg, err := storage.Config(); err == nil {
		var blank map[string]valueless
		if content, err := readGitDirFile(storage.Filesystem(), "config"); err == nil {
			blank = valuelessKeys(content)
		}
		if verr := repositoryFormat(path, cfg, blank, what); verr != nil {
			return nil, verr
		}
	}
	repo, err := git.Open(decidedFormat{storage}, wt)
	if err != nil {
		return nil, notARepo(err)
	}
	// The storage itself from here on, whose config is the whole file, and
	// which every capability here asks for by its type.
	repo.Storer = storage
	return repo, nil
}

// maxPointerBytes bounds a file that points at a directory — a `.git` file
// or a commondir — at what git itself reads of one: it refuses a `.git` file
// over a megabyte as too large. One line is all either holds, and nothing
// but the caller who wrote it bounded how much of it was read.
const maxPointerBytes = 1 << 20

// readPointer is the content of name, a file that points at a directory, ""
// when there is no such file.
func readPointer(fs billy.Filesystem, name string) (string, error) {
	f, err := fs.Open(name)
	if errors.Is(err, iofs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	b, err := io.ReadAll(io.LimitReader(f, maxPointerBytes+1))
	if err != nil {
		return "", err
	}
	if len(b) > maxPointerBytes {
		return "", fmt.Errorf("%s is larger than git reads of one", name)
	}
	return string(b), nil
}

// gitDirPointer is the directory a `.git` file names, read as go-git reads
// it: the `gitdir: ` prefix, then the rest of the first line, trimmed.
func gitDirPointer(wt billy.Filesystem) (string, error) {
	content, err := readPointer(wt, gitDirName)
	if err != nil {
		return "", err
	}
	named, ok := strings.CutPrefix(content, "gitdir: ")
	if !ok {
		return "", errors.New(".git file has no gitdir: prefix")
	}
	named, _, _ = strings.Cut(named, "\n")
	return strings.TrimSpace(named), nil
}

// commonDir is the commondir file's content as openAt read it to open the
// directory it names, trimmed; "" when there is no such file, or none this
// reads.
func commonDir(fs billy.Filesystem) string {
	named, err := readPointer(fs, "commondir")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(named)
}

// alternatesInBounds judges each entry of objects/info/alternates, the third
// pointer, read the way git reads it: one path per line, a blank line or a
// `#` comment skipped, a relative path taken from the objects directory.
// go-git reads either kind inside the git directory instead and finds nothing
// there, so today such a repository fails as a missing object rather than
// answering from outside; judging the entry keeps it a refusal that says why,
// and keeps it a refusal if the library comes to read the entry the way git
// does.
//
// Line by line rather than read whole, as go-git scans it, since nothing but
// the caller who wrote it bounds how long it is, and the first entry out of
// bounds ends the reading. A file this cannot read to its end is refused
// rather than judged by the part before: a line longer than the scanner
// takes stopped the reading there, and every entry after it went unjudged.
func alternatesInBounds(req plugin.Request, fs billy.Filesystem) *view.Error {
	objects := filepath.Join(fs.Root(), "objects")
	f, err := fs.Open(filepath.Join("objects", "info", "alternates"))
	if errors.Is(err, iofs.ErrNotExist) {
		return nil
	}
	unreadable := func(err error) *view.Error {
		return view.Errorf("git.objects.unreadable", "%s: reading objects/info/alternates: %v", fs.Root(), err)
	}
	if err != nil {
		return unreadable(err)
	}
	defer func() { _ = f.Close() }()
	lines := bufio.NewScanner(f)
	for lines.Scan() {
		entry := strings.TrimSpace(lines.Text())
		if entry == "" || strings.HasPrefix(entry, "#") {
			continue
		}
		if _, verr := req.Confine("path", against(objects, entry)); verr != nil {
			return verr
		}
	}
	if err := lines.Err(); err != nil {
		return unreadable(err)
	}
	return nil
}

// Most of each file this reads from a git directory that go-git reads whole,
// or holds in memory whole once read, on every capability that opens the
// repository: the config (config.ReadConfig reads it with io.ReadAll), the
// index (every entry is decoded into memory before anything looks at one),
// and packed-refs (every line becomes a reference when refs are listed).
//
// **Nothing bounded them, and they are files a caller can write.** A sparse
// config of three gigabytes, which costs no disk, cost one git.config
// 7.2 GiB of memory before it was stopped; an index whose header claimed two
// billion entries over three sparse gigabytes cost one git.status 9.2 GiB;
// 256 MiB of packed-refs cost git.branches 1.6 GiB. Each bound is measured
// against the largest real repositories rather than guessed: Chromium's
// index, half a million files, is 71 MiB, and decoding it costs 120 MiB;
// the packed-refs of a kubernetes mirror carrying every pull request's two
// refs would be 7.6 MiB; the largest config across the 75 repositories on
// the machine this was written on is 466 KiB. HEAD and loose refs hold one
// line, and go-git reads each whole as well, so they are held to what git
// reads of a .git file (maxPointerBytes).
const (
	maxConfigBytes     = 4 << 20
	maxIndexBytes      = 128 << 20
	maxPackedRefsBytes = 64 << 20
)

// readLimit is the most this reads of name, in a git directory where gitDir
// is set and in a working tree where it is not, 0 for a file with no bound of
// its own there. The one file of a working tree go-git reads whole is its
// .gitmodules (gitmodules).
func readLimit(name string, gitDir bool) int64 {
	name = filepath.ToSlash(filepath.Clean(name))
	switch {
	case !gitDir:
		if name == gitmodules {
			return maxConfigBytes
		}
		return 0
	case name == "config", name == "config.worktree":
		return maxConfigBytes
	case name == "index":
		return maxIndexBytes
	case name == "packed-refs":
		return maxPackedRefsBytes
	case name == "HEAD" || strings.HasSuffix(name, "_HEAD") && !strings.Contains(name, "/"),
		strings.HasPrefix(name, "refs/"):
		return maxPointerBytes
	}
	return 0
}

// gitmodules is the file of a working tree that names its submodules, which
// go-git reads whole (Worktree.Submodules, with io.ReadAll) and parses as
// git config on every status, and so on every git.status, git.diff and
// git.overview.
//
// **It is a file anybody who can write a commit writes, and nothing bounded
// it.** A .gitmodules made sparse at 768 MiB, which costs its writer 20 KiB
// of disk, cost one git.status 2.9 GiB of memory. It is held to the bound
// the repository's own config is held to, being config too: Chromium's,
// naming 273 submodules, is 49 KiB, and the bound is room for eighty times
// as many.
const gitmodules = ".gitmodules"

// wholeFilesInBounds refuses a repository whose HEAD, index, config,
// packed-refs or .gitmodules is larger than this reads of one, before go-git
// reads any of them: the refusal names the file, where the read it prevents
// would have failed somewhere inside go-git as whatever capability was
// running, or not failed at all. dot is the git directory, shared the one its
// config and packed-refs are kept in, the common directory of a linked
// worktree, and work the working tree, nil for a bare repository. The open
// refuses the same files again (regularFiles), for one swapped in after this
// looked.
func wholeFilesInBounds(dot, shared, work billy.Filesystem) *view.Error {
	type file struct {
		fs     billy.Filesystem
		name   string
		gitDir bool
	}
	files := []file{{dot, "HEAD", true}, {dot, "index", true}, {shared, "config", true}, {shared, "packed-refs", true}}
	if work != nil {
		files = append(files, file{work, gitmodules, false})
	}
	for _, f := range files {
		info, err := f.fs.Stat(f.name)
		if err != nil {
			continue //nolint:nilerr // no such file is nothing to bound, and one that cannot be opened is refused by the open, with its reason
		}
		if limit := readLimit(f.name, f.gitDir); info.Size() > limit {
			return view.Errorf("git.repository.toolarge", "%s is %s, larger than the %s this reads of it",
				filepath.Join(f.fs.Root(), f.name), format.Bytes(info.Size()), format.Bytes(limit)).
				WithHint("it is read whole, and git writes none near this size")
		}
	}
	return nil
}

// regularFiles is a filesystem that opens nothing for reading but a regular
// file, and never waits to open one: the open is non-blocking, which a named
// pipe honours and a file ignores, and what it reached is refused unless it
// is a file. A file go-git reads whole is also refused past its bound
// (readLimit), and read no further than it.
//
// **And it writes nothing.** Every capability here reads, and go-git wrote
// through this filesystem all the same: its status initialised a repository
// under .git/modules for a submodule the config named and nothing had
// cloned. The status no longer asks for one (submodulesOnDisk), and a write
// anything else in go-git would make is refused here, failing the call that
// made it rather than changing somebody's repository from a Read.
type regularFiles struct {
	billy.Filesystem
	// gitDir says this is a git directory, whose files readLimit bounds by
	// name: a working tree's own config or index is somebody's file, and
	// its .gitmodules the one it bounds there.
	gitDir bool
}

// errNotAFile is why regularFiles refuses a pipe, a socket, a device or a
// directory where a file was asked for, and errReadOnly why it refuses to
// write.
var (
	errNotAFile = errors.New("not a regular file")
	errReadOnly = errors.New("rta reads a repository and writes nothing to it")
)

func (f regularFiles) Open(name string) (billy.File, error) {
	return f.OpenFile(name, os.O_RDONLY, 0)
}

func (f regularFiles) OpenFile(name string, flag int, perm os.FileMode) (billy.File, error) {
	if flag&(os.O_WRONLY|os.O_RDWR|os.O_CREATE|os.O_TRUNC|os.O_APPEND) != 0 {
		return nil, &iofs.PathError{Op: "open", Path: name, Err: errReadOnly}
	}
	file, err := f.Filesystem.OpenFile(name, flag|syscall.O_NONBLOCK, perm)
	if err != nil {
		return nil, err
	}
	// Judged by the name, as the open found it: a billy file has no Stat of
	// its own. The window between the two is what this leaves open — a pipe
	// swapped in there is read non-blocking, and holds a call only while
	// something else holds its other end open.
	info, err := f.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		if err == nil {
			err = errNotAFile
		}
		return nil, &iofs.PathError{Op: "open", Path: name, Err: err}
	}
	limit := readLimit(name, f.gitDir)
	if limit == 0 {
		return file, nil
	}
	if info.Size() > limit {
		_ = file.Close()
		return nil, &iofs.PathError{Op: "open", Path: name, Err: tooLarge(limit)}
	}
	// And read no further than the bound, whatever the name leads to by the
	// time it is read: the size above is the name's, not the open file's.
	return &boundedFile{File: file, limit: limit}, nil
}

// tooLarge is why a file in a git directory past its bound is not read.
func tooLarge(limit int64) error {
	return fmt.Errorf("larger than the %s this reads of it", format.Bytes(limit))
}

// boundedFile is a file in a git directory that fails a read past limit
// rather than hand go-git more of it.
type boundedFile struct {
	billy.File
	limit, read int64
}

func (f *boundedFile) Read(p []byte) (int, error) {
	if left := f.limit + 1 - f.read; int64(len(p)) > left {
		p = p[:left]
	}
	n, err := f.File.Read(p)
	if f.read += int64(n); f.read > f.limit {
		return 0, &iofs.PathError{Op: "read", Path: f.Name(), Err: tooLarge(f.limit)}
	}
	return n, err
}

func (f *boundedFile) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > f.limit {
		return 0, &iofs.PathError{Op: "read", Path: f.Name(), Err: tooLarge(f.limit)}
	}
	return f.File.ReadAt(p, off)
}

func (f regularFiles) Create(name string) (billy.File, error) {
	return nil, &iofs.PathError{Op: "create", Path: name, Err: errReadOnly}
}

func (f regularFiles) TempFile(dir, _ string) (billy.File, error) {
	return nil, &iofs.PathError{Op: "createtemp", Path: dir, Err: errReadOnly}
}

func (f regularFiles) Rename(from, _ string) error {
	return &iofs.PathError{Op: "rename", Path: from, Err: errReadOnly}
}

func (f regularFiles) Remove(name string) error {
	return &iofs.PathError{Op: "remove", Path: name, Err: errReadOnly}
}

func (f regularFiles) MkdirAll(name string, _ os.FileMode) error {
	return &iofs.PathError{Op: "mkdir", Path: name, Err: errReadOnly}
}

func (f regularFiles) Symlink(_, link string) error {
	return &iofs.PathError{Op: "symlink", Path: link, Err: errReadOnly}
}

func (f regularFiles) Chroot(path string) (billy.Filesystem, error) {
	inner, err := f.Filesystem.Chroot(path)
	if err != nil {
		return nil, err
	}
	// A git directory's subdirectory keeps the bounds: the one go-git opens
	// this way is a submodule's repository under modules/, whose config and
	// HEAD it reads as it reads the superproject's.
	return regularFiles{Filesystem: inner, gitDir: f.gitDir}, nil
}

// against is p as git resolves it from dir: as it is when absolute, joined
// onto dir when not.
func against(dir, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dir, p)
}

// gitDirName is the entry that marks a checkout's root — a directory in the
// ordinary case, a file for a worktree or a submodule, which is why repoRoot
// stats it rather than asking whether it is a directory.
const gitDirName = ".git"

// repoRoot finds the repository directory a path belongs to, the way go-git's
// DetectDotGit would, and asks the host about every directory it considers on
// the way.
//
// The ask is what makes the walk safe to do at all. A boundary that confined
// the argument cannot confine the ancestors of the argument, and those are
// where the repository usually is; refusing at the first out-of-bounds
// ancestor stops the walk with a message that names the real reason, rather
// than with "not a git repository", which is what an operator would then spend
// an afternoon on.
//
// Each directory is asked what git's own discovery asks of it, in its order:
// whether it holds a .git, and then whether it is a git directory itself. A
// bare repository is its own git directory and has no .git entry to find, and
// this found one only by walking to the top of the filesystem and handing the
// path back — which a confined walk never reaches, since it stops at the
// first directory above the roots: over MCP a bare repository under a root
// was refused as outside it, naming the root's parent. A walk that reaches
// the top without finding anything still hands the path back unchanged, and
// it fails as "not a git repository" where it always did.
func repoRoot(req plugin.Request, path string) (string, *view.Error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", view.Errorf("git.path.invalid", "%s: %v", path, err)
	}
	for cur := abs; ; {
		checked, verr := req.Confine("path", cur)
		if verr != nil {
			return "", verr
		}
		if _, err := os.Stat(filepath.Join(checked, gitDirName)); err == nil || isGitDir(checked) {
			return checked, nil
		}
		parent := filepath.Dir(checked)
		if parent == checked {
			return abs, nil
		}
		cur = parent
	}
}

// isGitDir reports whether dir is itself a git directory, by the signs git's
// discovery reads: a HEAD, and the objects and refs directories, or a
// commondir saying where those are kept. Nothing is opened to tell — a named
// pipe for a HEAD would hold the walk, as openAt explains.
func isGitDir(dir string) bool {
	head, err := os.Lstat(filepath.Join(dir, "HEAD"))
	if err != nil || (!head.Mode().IsRegular() && head.Mode()&os.ModeSymlink == 0) {
		return false
	}
	if common, err := os.Lstat(filepath.Join(dir, "commondir")); err == nil && common.Mode().IsRegular() {
		return true
	}
	for _, sub := range []string{"objects", "refs"} {
		if info, err := os.Stat(filepath.Join(dir, sub)); err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// fileHelp is the help of the file input git.blame and git.log take, which
// says what repoFile does with it.
func fileHelp(what string) string {
	return what + ", relative to the current directory as git takes it — or, in a repository " +
		"with no checkout here (a URL, a bare repository), to the repository's root"
}

// repoFile is the file input git.blame and git.log take, the way go-git wants
// it: relative to the repository root, with forward slashes.
//
// **A path like any other, taken from the current directory as git takes
// one.** The input is a path, so the boundary resolves it against the
// server's working directory and judges it before a handler sees it, the way
// it resolves every path an agent sends — while the help said "relative to
// the repository root", and the CLI, which resolves nothing, took it that
// way. With a root above the checkout, {path: repo, file: README} was judged
// as <root>/README and refused as outside the repository, with a hint to name
// a file under the repository root, which is what the caller had done, and
// the CLI took README and refused repo/README: two surfaces wanting opposite
// inputs, and the help describing one of them.
//
// Taking the file from the repository root on every surface would have meant
// a file input the boundary does not judge, left to this handler to put to
// the gate, and every path an agent can send is one the boundary judges for
// a reason (internal/mcp's TestNoRemoteInputSmellsLikeAPathWithoutBeingOne).
// So every surface takes it the boundary's way, which is also git's: `git
// blame` and `git log -- <path>` take a path from the current directory, and
// in a subdirectory of a checkout the file beside you is named as it is.
//
// A repository with no checkout on this disk is the exception, because there
// is nowhere here for a path to lead: a remote URL cloned into memory, which
// only a terminal may name, and a bare repository. There the file is named in
// the repository, from its root, and an absolute one has nowhere to be placed.
// Over MCP the boundary has already made a relative file absolute, from the
// current directory as it makes every path, so there it is taken back to the
// relative one the caller sent — without that, a bare repository's file could
// not be named over MCP at all, and the refusal told the caller to send what
// it had. spelled is how the caller's surface names the input, for the hint.
func repoFile(repo *git.Repository, file string, surface plugin.Surface, spelled string) (string, *view.Error) {
	_, onDisk := repo.Storer.(*filesystem.Storage)
	wt, err := repo.Worktree()
	if !onDisk || err != nil {
		if cwd, cerr := os.Getwd(); cerr == nil && surface == plugin.SurfaceMCP && filepath.IsAbs(file) {
			if rel, rerr := filepath.Rel(realPath(cwd), file); rerr == nil && !climbsOut(rel) {
				file = rel
			}
		}
		if rel := filepath.Clean(file); !filepath.IsAbs(file) && !climbsOut(rel) {
			return filepath.ToSlash(rel), nil
		}
		return "", view.Errorf("git.file.outside", "%s: this repository has no checkout here to place it in", file).
			WithHint("give " + spelled + " as the repository names it, from its root")
	}
	root := wt.Filesystem.Root()
	abs, err := filepath.Abs(file)
	if err != nil {
		return "", view.Errorf("git.path.invalid", "%s: %v", file, err)
	}
	rel, err := filepath.Rel(realPath(root), filepath.Join(realPath(filepath.Dir(abs)), filepath.Base(abs)))
	if err != nil || rel == "." || climbsOut(rel) {
		return "", view.Errorf("git.file.outside", "%s is not inside the repository at %s", abs, root).
			WithHint("give " + spelled + " as a path to a file in that repository: a relative one is " +
				"taken from the directory rta runs in, as git takes one, and not from the repository")
	}
	return filepath.ToSlash(rel), nil
}

// realPath is p with the symlinks in it resolved, as far as it exists: the
// working tree go-git opened is a real path, and the current directory, as
// the shell hands it over, often is not — /tmp is /private/tmp on macOS, and
// a checkout under a linked directory is an ordinary one. The file's own name
// is kept, joined back on by the caller, because a tracked symlink is blamed
// as the link and not as what it points at. A part that does not exist yet —
// a file log is asked about that has since been deleted — is kept as spelled.
func realPath(p string) string {
	for rest := ""; ; {
		if resolved, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(p)
		if parent == p {
			return filepath.Join(p, rest)
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// climbsOut reports whether a cleaned relative path starts above the
// directory it is relative to.
func climbsOut(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// shortHash is the 7-character abbreviation `git log --oneline` and
// `git show` both use, long enough in practice to stay unambiguous in any
// repository small enough for this plugin's other limits to matter.
func shortHash(h fmt.Stringer) string {
	s := h.String()
	if len(s) > 7 {
		return s[:7]
	}
	return s
}
