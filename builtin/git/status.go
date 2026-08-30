package git

import (
	"context"
	"sort"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
	status, err := wt.Status()
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
