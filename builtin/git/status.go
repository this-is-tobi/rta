package git

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	pathpkg "path"
	"sort"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func statusCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.status",
		Summary:      "What has changed in the working tree, staged and unstaged",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "The structured equivalent of `git status --porcelain`: every path with a " +
			"staged change, an unstaged change, or neither yet — added, tracked at all — one row " +
			"per path, both halves shown side by side rather than requiring the two-column code to " +
			"be decoded by eye. It ignores what git ignores, matching each pattern as git's own matcher " +
			"does: each .gitignore, the repository's info/exclude, and the file core.excludesFile names, " +
			"~/.config/git/ignore by default; at most 1 MiB and 10000 patterns of them in all, in the " +
			"order it reads them. One past that is not applied, as git applies no pattern file past " +
			"100 MB, and neither is a .gitignore that is a symbolic link, which git does not follow: " +
			"what it ignores is listed, and a warning names it. It reads the working tree for at most two " +
			"seconds, and past them is refused as git.status.timeout rather than answered with the part " +
			"it had read, which would read as a cleaner tree than the one there.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runStatus,
	}
}

// statusLetters mirrors `git status --porcelain`'s own two-column
// vocabulary exactly, so a row here reads the same way to someone who
// already knows the CLI's output.
var statusLetters = map[git.StatusCode]string{
	git.Unmodified:         " ",
	git.Untracked:          "?",
	git.Modified:           "M",
	typeChanged:            "T",
	git.Added:              "A",
	git.Deleted:            "D",
	git.Renamed:            "R",
	git.Copied:             "C",
	git.UpdatedButUnmerged: "U",
}

func statusLetter(c git.StatusCode) string {
	if s, ok := statusLetters[c]; ok {
		return s
	}
	return "?"
}

func runStatus(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()
	wt, err := repo.Worktree()
	if err != nil {
		return nil, view.Errorf("git.status.worktree", "no working tree here: %v", err).
			WithHint("a bare repository has no working tree to report on")
	}
	status, ignored, err := worktreeStatus(ctx, statusDeadline(ctx), repo, wt, req)
	if err != nil {
		return nil, statusFailed("git.status.failed", err)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Path"},
		{Name: "Staged"},
		{Name: "Worktree"},
	}, Empty: "nothing to commit, working tree clean"}
	paths := make([]string, 0, len(status))
	for p := range status {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		fs := status[p]
		t.Rows = append(t.Rows, []string{p, statusLetter(fs.Staging), statusLetter(fs.Worktree)})
	}
	t.Total = len(t.Rows)
	if len(ignored) > 0 {
		t.Warnings = append(t.Warnings, view.Error{
			Code:    "git.status.ignore",
			Message: ignored.sentence(format.Plural(len(ignored), "what it ignores", "what they ignore") + " is listed as untracked"),
			Hint:    "`git status` lists what git ignores; git itself applies none of a pattern file past 100 MB",
		})
	}
	return t, nil
}

// worktreeStatus is wt's status, as go-git's Worktree.Status reads it but
// through submodulesOnDisk, so that reading it writes nothing, with no
// ignore file of go-git's applying (statusFiles), and each untracked path it
// lists put to git's own rules instead (ignoresRead), which read the ignore
// files git applies at the root that go-git does not find
// (rootExcludeSources) and hold every ignore file to what one status
// applies; and with a change of kind told apart from a change of content
// (kindChanges). The ignore files it did not apply come with it. req is the
// call, whose path gate a file the config names is put to. Every capability
// that reports the working tree's state asks for it here.
//
// All of it is held to deadline, and to ctx, and refused past either
// (statusBudget): the error is then the refusal, a *view.Error, which the
// caller hands on as it is (statusFailed).
func worktreeStatus(ctx context.Context, deadline time.Time, repo *git.Repository, wt *git.Worktree,
	req plugin.Request,
) (git.Status, unapplied, error) {
	storer := repo.Storer
	store, onDisk := repo.Storer.(*filesystem.Storage)
	if onDisk {
		storer = submodulesOnDisk{store}
	}
	root := wt.Filesystem.Root()
	budget := &statusBudget{ctx: ctx, deadline: deadline}
	configs, cerr := gitConfigs(ctx, req, repo)
	read := newIgnoresRead(rootExcludeSources(repo, configs, cerr, root, pathGateOf(req)),
		cerr == nil && ignoreCase(configs), wt.Filesystem, budget)
	reader, err := git.Open(storer, statusFiles{Filesystem: wt.Filesystem, budget: budget})
	if err != nil {
		return nil, nil, err
	}
	bounded, err := reader.Worktree()
	if err != nil {
		return nil, nil, err
	}
	bounded.Excludes = []gitignore.Pattern{keepEverything{}}
	status, err := bounded.Status()
	// Looked at before go-git's own error, which a read refused for the
	// budget made, and after a status it answered, where go-git passed over a
	// read refused so: a file it could not open to hash is one it calls
	// modified.
	if verr := budget.refusal(root); verr != nil {
		return nil, nil, verr
	}
	if err != nil {
		return nil, nil, err
	}
	read.dropIgnored(status)
	if idx, err := repo.Storer.Index(); err == nil {
		restoreRootIgnore(wt.Filesystem, idx, status, budget)
	}
	if onDisk {
		tree, err := worktreeDir(req, wt)
		if err != nil {
			return nil, nil, err
		}
		kindChanges(repo, tree, status, budget)
	}
	if verr := budget.refusal(root); verr != nil {
		return nil, nil, verr
	}
	return status, read.unapplied(), nil
}

// statusFailed is why a status could not be read, as a capability reports it:
// the refusal worktreeStatus made where it made one, and code with go-git's
// reason otherwise.
func statusFailed(code string, err error) *view.Error {
	var verr *view.Error
	if errors.As(err, &verr) {
		return verr
	}
	return view.Errorf(code, "reading status: %v", err)
}

// statusBudget is the time one call spends reading the working tree's
// status, and what it had read when the time ran out, for the refusal to
// say.
//
// **go-git's status costs what the working tree holds, and nothing bounded
// it.** It lists every directory, hashes every file whose timestamp no longer
// matches the index's, and walks every untracked directory, ignored or not; a
// hundred thousand rewritten files cost one git.status 7 s of CPU, where git
// takes 1.4 s. And matching the untracked files against ignore patterns costs
// the files times the patterns, within the bounds on either: ten thousand of
// each, the patterns heavy with stars, cost 30 s (lastMatch).
//
// So a call reads the working tree for statusTime, or until the caller stops
// waiting, and each read and each step of matching looks at the clock
// (statusFiles, lastMatch, wildmatch). Past it nothing more is read, and the
// call is refused rather than answered from what it had read: a status with
// part of the tree unread is a cleaner tree than the one there, its unread
// changes missing and the files it had not yet matched listed as untracked,
// a secret among them for git.diff to show.
type statusBudget struct {
	ctx      context.Context
	deadline time.Time
	over     error
	steps    int
	// What the status had read when the time ran out: the directories it
	// listed, the files it opened, and, once go-git had walked the tree, the
	// untracked files it had to match and how many it had.
	dirs, files, untracked, matched int
}

// statusTime is how long one call reads the working tree's status for: the
// two seconds a call matches lines in (matchTime) and a blame walks its
// history in, the one budget of time a call here has for work in proportion
// to what a caller can write. It is a clean checkout of some two hundred
// thousand files, twice the Linux kernel's; Chromium's half million is past
// it, and `git status` reads that one. git.diff matches its lines in what the
// status leaves of its own two seconds, as it did before the status was held
// to them. A variable so a test can lower it.
var statusTime = 2 * time.Second

// statusDeadline is when a call stops reading the working tree's status:
// statusTime from now, or the caller's own deadline when that comes first.
func statusDeadline(ctx context.Context) time.Time {
	deadline := time.Now().Add(statusTime)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		return d
	}
	return deadline
}

// errStatusTime is a read the status's budget no longer allows.
var errStatusTime = errors.New("the status ran past its time")

// past reports whether the call has run past its deadline, or its caller
// stopped waiting for it, and remembers which the first time. A nil budget
// never runs out.
func (b *statusBudget) past() bool {
	if b == nil {
		return false
	}
	if b.over == nil {
		switch {
		case b.ctx.Err() != nil:
			b.over = b.ctx.Err()
		case !time.Now().Before(b.deadline):
			b.over = errStatusTime
		}
	}
	return b.over != nil
}

// tick is past for one step of matching, which costs so much less than
// reading the clock that the clock is read at every 256th.
func (b *statusBudget) tick() bool {
	switch {
	case b == nil:
		return false
	case b.over != nil:
		return true
	}
	if b.steps++; b.steps%256 != 0 {
		return false
	}
	return b.past()
}

// refuse is the error a read the budget no longer allows fails with.
func (b *statusBudget) refuse(op, path string) error {
	return &iofs.PathError{Op: op, Path: path, Err: b.over}
}

// refusal is the call refused for its budget, on the working tree at root,
// saying what it had read by then; nil where the budget has not run out.
func (b *statusBudget) refusal(root string) *view.Error {
	switch {
	case b.over == nil:
		return nil
	case !errors.Is(b.over, errStatusTime):
		return view.Errorf("git.status.cancelled", "the status of the working tree at %s was interrupted", root)
	}
	read := fmt.Sprintf("it had listed %s and opened %s of it by then", format.CountOf(b.dirs, "directory"),
		format.CountOf(b.files, "file"))
	if b.matched < b.untracked {
		read = fmt.Sprintf("it had matched %d of its %s against the ignore patterns by then", b.matched,
			format.CountOf(b.untracked, "untracked file"))
	}
	return view.Errorf("git.status.timeout", "the status of the working tree at %s took longer than the %v one "+
		"call spends reading it: %s", root, statusTime, read).
		WithHint("`git status` reads it at a terminal; rta refuses rather than answer with the part it had " +
			"read, which would show a cleaner tree than the one there")
}

// pathGateOf is the host's path gate as a status puts a file it derives to
// it: the path the gate judged, or why it refuses it.
func pathGateOf(req plugin.Request) func(string) (string, *view.Error) {
	return func(p string) (string, *view.Error) { return req.Confine("path", p) }
}

// typeChanged is the code git gives a path whose kind changed, which go-git's
// status has no code for.
const typeChanged git.StatusCode = 'T'

// kindChanges marks T each path of status whose kind changed where go-git
// marked it M: between HEAD and the index for the staged half, between the
// index and what is on disk for the other, as `git status --porcelain` marks
// them.
//
// **A kind is git's, not the filesystem's.** git records a file, a symbolic
// link or a submodule, and it takes anything on disk that is not a link or a
// directory for a file: a named pipe in place of a tracked file is M in git,
// and a pipe in place of a tracked link is T. The pipe is judged by a lstat
// and never opened — open(2) on one with no writer waits for one, which no
// context interrupts (readWorktreeEntry) — and go-git's status did not open
// it either, finding no mode of git's for it.
//
// Only a path already marked M is looked at, so a clean tree costs nothing
// more; the index is read again for one that is not. And what is on disk is
// read a directory at a time, the kind of each entry coming with its name,
// where a lstat of each path doubled the cost of a status of a hundred
// thousand rewritten files, 14 s on top of go-git's 5 s. Each listing is held to the
// call's budget, as the status's own are (statusBudget).
func kindChanges(repo *git.Repository, tree boundDir, status git.Status, budget *statusBudget) {
	var modified []string
	staged := false
	for path, fs := range status {
		if fs.Staging == git.Modified || fs.Worktree == git.Modified {
			modified = append(modified, path)
			staged = staged || fs.Staging == git.Modified
		}
	}
	if len(modified) == 0 {
		return
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return
	}
	entryOf := indexLookup(idx)
	var head *headFiles
	if staged {
		head = headFilesOf(repo)
	}
	kinds := map[string]map[string]os.FileMode{}
	for _, path := range modified {
		entry := entryOf(path)
		if entry == nil {
			continue
		}
		fs := status[path]
		if fs.Staging == git.Modified {
			if was, _ := head.entry(path); was != nil && kindOf(was.Mode) != kindOf(entry.Mode) {
				fs.Staging = typeChanged
			}
		}
		if fs.Worktree == git.Modified {
			if kind, ok := entryKind(kinds, tree, path, budget); ok && !kind.IsDir() && diskKind(kind) != kindOf(entry.Mode) {
				fs.Worktree = typeChanged
			}
		}
	}
}

// entryKind is the kind of what is on disk at path, as its directory's
// listing gives it without a lstat, and whether there is anything there;
// kinds holds each directory's listing, read once. The directories on the way
// are real ones for a path go-git's status marked M, which does not walk
// through a link; over MCP each listing is read from the directory the
// working tree is held open by (worktreeDir), and a directory swapped for a
// link out of it since the status is no listing at all.
func entryKind(kinds map[string]map[string]os.FileMode, tree boundDir, path string, budget *statusBudget) (
	os.FileMode, bool,
) {
	dir, name := pathpkg.Split(path)
	listing, ok := kinds[dir]
	if !ok {
		if budget.past() {
			return 0, false
		}
		budget.dirs++
		listing = map[string]os.FileMode{}
		if entries, err := tree.ReadDir(dir); err == nil {
			for _, e := range entries {
				listing[e.Name()] = e.Type()
			}
		}
		kinds[dir] = listing
	}
	kind, ok := listing[name]
	return kind, ok
}

// kindOf is the kind git records an entry of mode as: a file, executable or
// not, a symbolic link, or a submodule.
func kindOf(mode filemode.FileMode) filemode.FileMode {
	switch mode {
	case filemode.Symlink, filemode.Submodule:
		return mode
	}
	return filemode.Regular
}

// diskKind is the kind git takes what is on disk for, a directory aside: a
// symbolic link, and a file for anything else, a pipe or a device included.
func diskKind(mode os.FileMode) filemode.FileMode {
	if mode&os.ModeSymlink != 0 {
		return filemode.Symlink
	}
	return filemode.Regular
}

// submodulesOnDisk is a repository's storage whose config names a submodule
// only where .git/modules holds a repository for it, for the status to read.
//
// **go-git's status wrote.** For each submodule the config names it asks
// Submodule.Repository for the submodule's HEAD, and that initialises a
// repository under .git/modules/<name> when it finds none there: a config,
// a HEAD, objects and refs, and a .git file in the submodule's directory. A
// checkout whose submodules were initialised and never cloned, which is what
// `git submodule init` alone leaves, or one whose submodule keeps its own
// repository in its directory as older git did, had git.status, git.diff and
// git.overview each write into it, from capabilities that read. The
// repository it made was empty with an unborn HEAD, so the status read the
// submodule at the commit the index records, which is what it reads for a
// submodule the config does not name. Left out of the config the status
// reads, such a submodule is read the same, and nothing is written. The
// config is the one openAt hands git.Open, too (decidedFormat), since the
// status opens the repository a second time through it.
type submodulesOnDisk struct{ *filesystem.Storage }

func (s submodulesOnDisk) Config() (*config.Config, error) {
	cfg, err := decidedFormat(s).Config()
	if err != nil {
		return nil, err
	}
	for name := range cfg.Submodules {
		if module, err := s.Module(name); err == nil {
			if _, err := module.Reference(plumbing.HEAD); err == nil {
				continue
			}
		}
		delete(cfg.Submodules, name)
	}
	return cfg, nil
}
