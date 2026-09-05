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

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
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
