package git

import (
	"context"
	"sort"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func branchesCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.branches",
		Summary:    "Local branches, and which one is checked out",
		Safety:     plugin.Read,
		Idempotent: true,
		Description: "Every local branch, alphabetically, with the checked-out one marked. A " +
			"detached HEAD — checked out at a commit rather than a branch — is reported as its " +
			"own row rather than left for the caller to notice no branch was marked.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runBranches,
	}
}

func runBranches(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}

	head, err := repo.Head()
	detached := false
	var currentBranch string
	switch {
	case err != nil:
		// No commits yet (an empty repository) or no HEAD at all — neither
		// is an error the caller did anything wrong to reach, so this is
		// reported as zero branches rather than a failure.
	case head.Name().IsBranch():
		currentBranch = head.Name().Short()
	default:
		detached = true
	}

	iter, err := repo.Branches()
	if err != nil {
		return nil, view.Errorf("git.branches.failed", "listing branches: %v", err)
	}
	defer iter.Close()

	t := view.Table{Columns: []view.Column{
		{Name: "Name"},
		{Name: "Current"},
	}}
	names := map[string]bool{}
	err = iter.ForEach(func(ref *plumbing.Reference) error {
		names[ref.Name().Short()] = true
		return nil
	})
	if err != nil {
		return nil, view.Errorf("git.branches.failed", "listing branches: %v", err)
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	for _, n := range sorted {
		current := ""
		if n == currentBranch {
			current = "yes"
		}
		t.Rows = append(t.Rows, []string{n, current})
	}
	if detached {
		t.Rows = append([][]string{{"(detached at " + shortHash(head.Hash()) + ")", "yes"}}, t.Rows...)
	}
	t.Total = len(t.Rows)
	return t, nil
}
