package git

import (
	"context"
	"errors"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func hooksCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.hooks",
		Summary:      "What's in the hooks directory, and which of it git would actually run",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Every entry in the directory git runs this repository's hooks from — " +
			"core.hooksPath when any config git reads sets it, the repository's own hooks directory " +
			"otherwise — judged by the same rule git itself uses to decide whether one fires on " +
			"commit, push and the rest: named exactly (a `.sample` suffix never runs) and executable. " +
			"A hook is an arbitrary script that runs on this machine, so this reports what would " +
			"actually execute, and where each file is, not merely what a directory listing shows.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runHooks,
	}
}

func runHooks(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepoConfigOnly(ctx, req)
	if verr != nil {
		return nil, verr
	}

	fs, verr := hooksFilesystem(repo)
	if verr != nil {
		return nil, verr
	}
	dir, base, err := hooksDir(repo, fs)
	if err != nil {
		return nil, view.Errorf("git.hooks.failed", "reading core.hooksPath from the operator's git config: %v", err)
	}
	// A directory this handler derives, from a config a caller can write
	// inside the root and from the operator's own, so it is put back to the
	// host as repoRoot puts back the repository: a core.hooksPath naming
	// ~/.githooks from a root drawn around one project lists nothing outside
	// it. And read at the place the host judged, symlinks resolved, so a
	// hooks directory linked out of the root is refused rather than followed.
	judged, verr := req.Confine("path", dir)
	if verr != nil {
		return nil, verr
	}
	// os.ReadDir returns entries already sorted by name.
	entries, err := os.ReadDir(judged)
	if err != nil && !errors.Is(err, iofs.ErrNotExist) {
		return nil, view.Errorf("git.hooks.failed", "reading hooks in %s: %v", shownFrom(base, dir), err)
	}

	t := view.Table{
		Columns: []view.Column{
			{Name: "Name"},
			{Name: "Status", Kind: view.KindStatus},
			{Name: "Path"},
		},
		Empty: "no hooks in " + shownFrom(base, dir),
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		name := e.Name()
		status := "disabled"
		switch {
		case strings.HasSuffix(name, ".sample"):
			status = "sample"
		case info.Mode()&0o111 != 0:
			status = "active"
		}
		t.Rows = append(t.Rows, []string{
			strings.TrimSuffix(name, ".sample"), status, shownFrom(base, filepath.Join(dir, name)),
		})
	}
	t.Total = len(t.Rows)
	return t, nil
}

// hooksDir is the directory git runs this repository's hooks from, and the
// directory git runs them in, which a relative one is taken from and which a
// path is shown from.
//
// **core.hooksPath first, from every config git reads.** git reads the hooks
// directory only when core.hooksPath is unset in the repository's config, the
// operator's global one and the system's — husky sets it, and so do many
// organisations for every repository at once. This listed the hooks directory
// whatever the config said, and answered "no active hooks" for a repository
// whose pre-commit git ran on every commit. The capability is the answer
// somebody auditing a checkout relies on for what would execute, so the
// operator's own config is read for this one key on every surface, MCP
// included — git.config withholds those scopes there because they hold
// credentials, and a directory is not one; leaving it unread would answer
// wrongly instead, and the directory it names is put to the host like any
// other. An include in any of those files is not followed, as git.config
// does not follow one.
//
// A relative value is taken from where git runs a hook, the working tree's
// root, or the git directory itself in a bare repository; ~ is the
// operator's home, as git expands it. Unset, it is the hooks directory of
// the common git directory, which a linked worktree shares with its main
// checkout.
func hooksDir(repo *git.Repository, fs billy.Filesystem) (dir, base string, err error) {
	base = fs.Root()
	if wt, werr := repo.Worktree(); werr == nil {
		base = wt.Filesystem.Root()
	}
	value, err := hooksPathSetting(repo)
	if err != nil {
		return "", "", err
	}
	if value == "" {
		return filepath.Join(commonGitDir(fs), "hooks"), base, nil
	}
	return against(base, plugin.ExpandHome(value)), base, nil
}

// hooksPathSetting is core.hooksPath as git resolves it: the repository's
// config over the operator's global one over the system's, "" where none
// sets it.
func hooksPathSetting(repo *git.Repository) (string, error) {
	if local, err := repo.Config(); err == nil {
		if v := local.Raw.Section("core").Option("hooksPath"); v != "" {
			return v, nil
		}
	}
	machine, err := machineConfigs()
	if err != nil {
		return "", err
	}
	for i := len(machine) - 1; i >= 0; i-- {
		if v := machine[i].config.Raw.Section("core").Option("hooksPath"); v != "" {
			return v, nil
		}
	}
	return "", nil
}

// shownFrom is p as a row shows it: from base when it is inside, which the
// repository's own hooks directory and a hooksPath in the checkout are, and
// in full when it is not.
func shownFrom(base, p string) string {
	if rel, err := filepath.Rel(base, p); err == nil && !climbsOut(rel) {
		return filepath.ToSlash(rel)
	}
	return p
}

// commonGitDir is the directory a git directory keeps its objects, refs,
// config and hooks in: the one its commondir file names, and itself when it
// has none.
func commonGitDir(fs billy.Filesystem) string {
	if named := commonDir(fs); named != "" {
		return against(fs.Root(), named)
	}
	return fs.Root()
}

// hooksFilesystem returns the billy.Filesystem hooks live under — split out
// from runHooks so a memory-backed clone's refusal can be tested directly
// against a real *git.Repository (built via cloneRepo) without needing a
// reachable remote host to drive it through openRepo's own routing.
//
// Hooks are files on disk, exec'd by the git binary itself — go-git never
// consults them, and a remote path here was never written to disk at all
// (cloneRepo keeps everything in memory), so there is nothing to list.
func hooksFilesystem(repo *git.Repository) (billy.Filesystem, *view.Error) {
	fss, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil, view.Errorf("git.hooks.unavailable", "no hooks directory for this repository").
			WithHint("hooks live on disk — a remote URL is cloned entirely in memory and never gets one")
	}
	return fss.Filesystem(), nil
}
