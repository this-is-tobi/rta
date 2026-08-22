package git

import (
	"context"
	"os"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func hooksCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.hooks",
		Summary:    "What's in the hooks directory, and which of it git would actually run",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Every entry in the repository's hooks directory, judged by the same rule " +
			"git itself uses to decide whether one fires on commit, push and the rest: named " +
			"exactly (a `.sample` suffix never runs) and executable. A hook is an arbitrary script " +
			"that runs on this machine, so this reports what would actually execute, not merely " +
			"what a directory listing shows.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runHooks,
	}
}

func runHooks(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}

	fs, verr := hooksFilesystem(repo)
	if verr != nil {
		return nil, verr
	}

	// ReadDir routes through the same commondir-aware resolution every other
	// dotgit path does (storage/filesystem/dotgit.RepositoryFilesystem), so
	// this needs no separate handling for bare repositories or worktrees.
	// os.ReadDir (which this ultimately calls) returns entries already
	// sorted by name.
	entries, err := fs.ReadDir("hooks")
	if err != nil && !os.IsNotExist(err) {
		return nil, view.Errorf("git.hooks.failed", "reading hooks: %v", err)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Name"},
		{Name: "Status", Kind: view.KindStatus},
	}}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		status := "disabled"
		switch {
		case strings.HasSuffix(name, ".sample"):
			status = "sample"
		case e.Mode()&0o111 != 0:
			status = "active"
		}
		t.Rows = append(t.Rows, []string{strings.TrimSuffix(name, ".sample"), status})
	}
	t.Total = len(t.Rows)
	return t, nil
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
