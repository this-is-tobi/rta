package git

import (
	"context"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"

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
			"be decoded by eye.",
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
	repo, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, view.Errorf("git.status.worktree", "no working tree here: %v", err).
			WithHint("a bare repository has no working tree to report on")
	}
	status, err := worktreeStatus(repo, wt)
	if err != nil {
		return nil, view.Errorf("git.status.failed", "reading status: %v", err)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Path"},
		{Name: "Staged"},
		{Name: "Worktree"},
	}}
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
	return t, nil
}

// worktreeStatus is wt's status, as go-git's Worktree.Status reads it but
// through submodulesOnDisk, so that reading it writes nothing. Every
// capability that reports the working tree's state asks for it here.
func worktreeStatus(repo *git.Repository, wt *git.Worktree) (git.Status, error) {
	store, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return wt.Status()
	}
	reader, err := git.Open(submodulesOnDisk{store}, wt.Filesystem)
	if err != nil {
		return nil, err
	}
	if wt, err = reader.Worktree(); err != nil {
		return nil, err
	}
	return wt.Status()
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
// reads, such a submodule is read the same, and nothing is written.
type submodulesOnDisk struct{ *filesystem.Storage }

func (s submodulesOnDisk) Config() (*config.Config, error) {
	cfg, err := s.Storage.Config()
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
