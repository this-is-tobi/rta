package git

import (
	"context"
	"errors"
	"fmt"
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
			"The config is read from every file git reads and from the environment, as git.config " +
			"reads them; a file one of them includes is not followed, and a warning says how many " +
			"were not. " +
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
	dir, base, warnings, err := hooksDir(repo, fs)
	if err != nil {
		return nil, view.Errorf("git.hooks.failed", "reading core.hooksPath from git's config: %v", err)
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
		Empty:    "no hooks in " + shownFrom(base, dir),
		Warnings: warnings,
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
// does not follow one, and the answer says so rather than being quietly
// wrong about a value one of them sets.
//
// A relative value is taken from where git runs a hook, the working tree's
// root, or the git directory itself in a bare repository; ~ is the
// operator's home, as git expands it. Unset, it is the hooks directory of
// the common git directory, which a linked worktree shares with its main
// checkout. warnings are what the answer cannot vouch for (hooksPathSetting).
func hooksDir(repo *git.Repository, fs billy.Filesystem) (dir, base string, warnings []view.Error, err error) {
	base = fs.Root()
	if wt, werr := repo.Worktree(); werr == nil {
		base = wt.Filesystem.Root()
	}
	value, warnings, err := hooksPathSetting(repo)
	if err != nil {
		return "", "", nil, err
	}
	if value == "" {
		return filepath.Join(commonGitDir(fs), "hooks"), base, warnings, nil
	}
	return against(base, plugin.ExpandHome(value)), base, warnings, nil
}

// hooksPathSetting is core.hooksPath as git resolves it, the value in the
// last of the files git reads that sets it (machineConfigSources, then the
// repository's own, then its worktree's), or in the environment, which git
// reads after them all (commandConfig), "" where none does; and a warning
// for each way the answer may not be the one the git that runs the hooks
// reaches.
//
// A file one of them includes is not read (includeCount), and could set it:
// the ones that count are included by the file the value came from or by a
// file read after it, which is all of them when none sets it. And two system
// files setting it differently are two builds of git running hooks from two
// directories, only one of which this lists: that is said, naming the files
// and not the values, since over MCP the operator's own config is read for
// this one key and a directory outside the root is not shown.
func hooksPathSetting(repo *git.Repository) (string, []view.Error, error) {
	files, err := machineConfigs()
	if err != nil {
		return "", nil, err
	}
	if local, err := repo.Config(); err == nil {
		files = append(files, scopedConfig{scope: "local", config: local})
		perWorktree, err := worktreeConfig(repo, local)
		if err != nil {
			return "", nil, fmt.Errorf("config.worktree: %w", err)
		}
		if perWorktree != nil {
			files = append(files, scopedConfig{scope: "worktree", config: perWorktree})
		}
	}
	command, err := commandConfig()
	if err != nil {
		return "", nil, err
	}
	if command != nil {
		files = append(files, scopedConfig{scope: "command", config: command})
	}
	from := -1
	var systems []string
	values := map[string]bool{}
	for i, f := range files {
		v := f.config.Raw.Section("core").Option("hooksPath")
		if v == "" {
			continue
		}
		from = i
		if f.scope == "system" {
			systems = append(systems, f.path)
			values[v] = true
		}
	}
	var warnings []view.Error
	if w := includesNotFollowed("git.hooks.include",
		"core.hooksPath was not looked for there, and git may run hooks from another directory",
		files[max(from, 0):]); w != nil {
		warnings = append(warnings, *w)
	}
	if from < 0 {
		return "", warnings, nil
	}
	if files[from].scope == "system" && len(values) > 1 {
		warnings = append(warnings, view.Error{
			Code: "git.hooks.system",
			Message: fmt.Sprintf("core.hooksPath is set differently in %s, which different builds of git "+
				"read as their system config: this lists the directory %s names",
				strings.Join(systems, ", "), files[from].path),
			Hint: "`git config --show-origin core.hooksPath` names the one the git on your PATH reads",
		})
	}
	return files[from].config.Raw.Section("core").Option("hooksPath"), warnings, nil
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
