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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/storage/memory"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
// local checkout is not always what is at hand, and every capability here
// already only reads.
func pathField(help string) plugin.Field {
	return plugin.Field{Name: "path", Type: plugin.Path, Positional: true, Default: ".",
		Help: help + " — or a remote URL (https://, ssh://, git@host:path), cloned in memory"}
}

// cloneTimeout bounds a remote clone. Not a Field: every capability here
// would need the same one, and the difference between "the URL is wrong"
// and "the network is slow" is not a distinction worth a person deciding
// per call — a generous, fixed budget covers both without asking.
const cloneTimeout = 60 * time.Second

// openRepo opens the repository at (or above) path, or clones it into memory
// if path names a remote instead of a local one.
//
// The two are told apart by reusing go-git's own endpoint parser rather than
// a hand-rolled prefix check: transport.NewEndpoint classifies a bare local
// path as protocol "file" and anything else (https://, ssh://, an
// scp-like git@host:path) as remote, which is the same classification the
// git binary itself would reach for the same string — so "does this look
// like a URL" is never answered twice, once here and once wrong.
func openRepo(ctx context.Context, req plugin.Request) (*git.Repository, *view.Error) {
	path := req.String("path")
	if ep, err := transport.NewEndpoint(path); err == nil && ep.Protocol != "file" {
		return cloneRepo(ctx, path)
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
	return repo, nil
}

// cloneRepo clones url entirely in memory — no working copy is left behind
// on disk, matching every capability here being Read: nothing is kept that
// would need cleaning up or could be found by someone else on the same
// machine. Only unauthenticated (public) URLs are supported in this first
// cut; a private repository fails with a clear reason rather than hanging
// on a credential prompt nobody is there to answer.
func cloneRepo(ctx context.Context, url string) (*git.Repository, *view.Error) {
	cctx, cancel := context.WithTimeout(ctx, cloneTimeout)
	defer cancel()
	repo, err := git.CloneContext(cctx, memory.NewStorage(), memfs.New(), &git.CloneOptions{URL: url})
	if err != nil {
		return nil, view.Errorf("git.clone.failed", "cloning %s: %v", url, err).
			WithHint("only unauthenticated (public) URLs are supported")
	}
	return repo, nil
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
