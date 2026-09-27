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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"

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
func openRepo(ctx context.Context, req plugin.Request) (*git.Repository, *view.Error) {
	return open(ctx, req, true)
}

// openRepoConfigOnly is the same repository without that refusal, for the
// three capabilities whose answers never come out of a packfile: `git config`
// reads .git/config, `git hooks` reads .git/hooks, and `git remotes` reads
// the refs and the configured remotes. An object database this reader can
// only see part of cannot make any of those wrong, and refusing them would
// report a fault in an answer that does not have one.
//
// A named opener rather than a flag on openRepo, so that a capability which
// grows an object read has to come here and change which one it calls.
func openRepoConfigOnly(ctx context.Context, req plugin.Request) (*git.Repository, *view.Error) {
	return open(ctx, req, false)
}

func open(ctx context.Context, req plugin.Request, readsObjects bool) (*git.Repository, *view.Error) {
	path := req.String("path")
	if gitclone.IsRemote(path) {
		if verr := gitclone.RefuseOverMCP(req, "repository"); verr != nil {
			return nil, verr
		}
		return gitclone.InMemory(ctx, path, gitclone.Options{})
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
		return nil, verr
	}
	// DetectDotGit is off because repoRoot has just done that walk under the
	// host's bound. With it off, go-git handles both remaining shapes from an
	// exact path: a checkout's root, whose .git it opens, and a bare
	// repository, which is its own git directory.
	repo, err := git.PlainOpenWithOptions(root, &git.PlainOpenOptions{
		EnableDotGitCommonDir: true,
	})
	if err != nil {
		return nil, view.Errorf("git.notarepo", "%s is not a git repository: %v", path, err).
			WithHint("run this against a directory inside a git repository, a checkout's own root, or a bare repository's own directory")
	}
	if verr := gitDirsInBounds(req, repo); verr != nil {
		return nil, verr
	}
	if readsObjects {
		if verr := objectsAllReadable(repo, root); verr != nil {
			return nil, verr
		}
	}
	return repo, nil
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

// gitDirsInBounds asks the host about every directory go-git reads this
// repository from, which repoRoot's walk never saw.
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
// read was never it.
//
// So the directories are put back to the host once go-git has resolved
// them, the way repoRoot puts back each level of its walk: the git directory
// by the root go-git opened it at, and the common one by the file go-git
// read to find it. A symlink inside either needs nothing here — go-billy's
// chroot refuses to follow a link out of the directory it was opened on, so
// `.git/objects` linked elsewhere fails as a crossed boundary on every
// surface. It is only the directories themselves go-git takes by name.
//
// objects/info/alternates is the third pointer, and is judged as git reads
// it: an absolute path as it is, a relative one against the objects
// directory. go-git reads either kind inside the git directory instead and
// finds nothing there, so today such a repository fails as a missing object
// rather than answering from outside; judging the entry keeps it a refusal
// that says why, and keeps it a refusal if the library comes to read the
// entry the way git does.
func gitDirsInBounds(req plugin.Request, repo *git.Repository) *view.Error {
	store, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil
	}
	fs := store.Filesystem()
	gitDir := fs.Root()
	if _, verr := req.Confine("path", gitDir); verr != nil {
		return verr
	}
	common := commonGitDir(fs)
	if common != gitDir {
		if _, verr := req.Confine("path", common); verr != nil {
			return verr
		}
	}
	return alternatesInBounds(req, fs, filepath.Join(common, "objects"))
}

// commonDir is the commondir file's content, read the way go-git reads it to
// open the directory it names: whole, and trimmed. Not bounded, because a
// bound is a second reading of the file — a path after a megabyte of
// whitespace is the path go-git opened, and a truncated read of that file
// would judge no path at all — and go-git has already read it whole to get
// here. "" when there is no such file.
func commonDir(fs billy.Filesystem) string {
	f, err := fs.Open("commondir")
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// alternatesInBounds judges each entry of objects/info/alternates, read the
// way git reads it: one path per line, a blank line or a `#` comment skipped,
// a relative path taken from the objects directory. Line by line rather than
// read whole, as go-git scans it, since nothing but the caller who wrote it
// bounds how long it is, and the first entry out of bounds ends the reading.
func alternatesInBounds(req plugin.Request, fs billy.Filesystem, objects string) *view.Error {
	f, err := fs.Open(filepath.Join("objects", "info", "alternates"))
	if err != nil {
		return nil
	}
	defer f.Close()
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
	return nil
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
// A walk that reaches the top of the filesystem without finding anything hands
// the path back unchanged: a bare repository is its own git directory and has
// no .git entry to find, and anything else fails as "not a git repository"
// where it always did.
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
		if _, err := os.Stat(filepath.Join(checked, gitDirName)); err == nil {
			return checked, nil
		}
		parent := filepath.Dir(checked)
		if parent == checked {
			return abs, nil
		}
		cur = parent
	}
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
// spelled is how the caller's surface names the input, for the hint.
func repoFile(repo *git.Repository, file, spelled string) (string, *view.Error) {
	_, onDisk := repo.Storer.(*filesystem.Storage)
	wt, err := repo.Worktree()
	if !onDisk || err != nil {
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
