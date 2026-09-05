package git

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func branchesCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.branches",
		Summary:      "Local branches, what each tracks, and which one is checked out",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Description: "Every local branch, alphabetically, with the checked-out one marked, the " +
			"remote branch it tracks, and how far the two have drifted as of the last fetch. " +
			"`gone` in Status means the branch is configured to track a remote branch this " +
			"repository no longer has a ref for — what `git fetch --prune` leaves behind " +
			"once the remote side was deleted, and the usual sign a merged branch can go. " +
			"With `--all`, the remote-tracking branches follow, spelled `remotes/<remote>/<name>` " +
			"the way `git branch -a` spells them. Nothing here touches the network: a " +
			"remote is reported as it stood the last time this repository fetched it. A " +
			"detached HEAD — checked out at a commit rather than a branch — is reported as its " +
			"own row rather than left for the caller to notice no branch was marked.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "all", Type: plugin.Bool, Config: "all", Help: "include remote-tracking branches"},
		},
		Run: runBranches,
	}
}

func runBranches(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, verr := openRepo(ctx, req)
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

	locals, remotes, err := branchRefs(repo)
	if err != nil {
		return nil, view.Errorf("git.branches.failed", "listing branches: %v", err)
	}

	t := view.Table{Columns: []view.Column{
		{Name: "Name"},
		{Name: "Current"},
		{Name: "Upstream"},
		{Name: "Status"},
	}}
	for _, ref := range locals {
		name := ref.Name().Short()
		current := ""
		if name == currentBranch {
			current = "yes"
		}
		upstream, status := upstreamStatus(repo, name, ref.Hash())
		t.Rows = append(t.Rows, []string{name, current, upstream, status})
	}
	if detached {
		t.Rows = append([][]string{{"(detached at " + shortHash(head.Hash()) + ")", "yes", "", ""}}, t.Rows...)
	}
	if req.Bool("all") {
		for _, ref := range remotes {
			t.Rows = append(t.Rows, []string{"remotes/" + ref.Name().Short(), "", "", ""})
		}
	}
	t.Total = len(t.Rows)
	return t, nil
}

// branchRefs returns the local and the remote-tracking branches, each sorted
// by name.
//
// One pass over every reference rather than repo.Branches() plus a second
// walk: the two lists come from the same namespace and the remote-tracking
// side needs a filter Branches() does not offer anyway. `origin/HEAD` is the
// one remote ref skipped — it is a pointer at another branch, not a branch,
// and `git branch -a` shows it only as an arrow.
func branchRefs(repo *git.Repository) (locals, remotes []*plumbing.Reference, err error) {
	refs, err := repo.References()
	if err != nil {
		return nil, nil, err
	}
	defer refs.Close()
	err = refs.ForEach(func(r *plumbing.Reference) error {
		switch {
		case r.Name().IsBranch():
			locals = append(locals, r)
		case r.Name().IsRemote() && r.Type() == plumbing.HashReference:
			remotes = append(remotes, r)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	byName := func(refs []*plumbing.Reference) {
		sort.Slice(refs, func(i, j int) bool { return refs[i].Name() < refs[j].Name() })
	}
	byName(locals)
	byName(remotes)
	return locals, remotes, nil
}

// upstreamStatus names what a branch tracks and how the two stand.
//
// A configured upstream whose remote-tracking ref is missing is `gone`, and
// that is decided from the branch's own config section rather than through
// upstreamOf: upstreamOf falls back to a same-named remote ref precisely so
// that a never-pushed clone still reports one, and that fallback would turn a
// deleted remote branch into "no upstream" — the one case this column exists
// to make visible. The counts are what `git branch -vv` prints, from the same
// bounded walks the overview tile uses.
func upstreamStatus(repo *git.Repository, branch string, tip plumbing.Hash) (upstream, status string) {
	if b, err := repo.Branch(branch); err == nil && b.Remote != "" {
		merge := branch
		if b.Merge != "" {
			merge = b.Merge.Short()
		}
		name := b.Remote + "/" + merge
		ref, err := repo.Reference(plumbing.NewRemoteReferenceName(b.Remote, merge), true)
		if err != nil {
			return name, "gone"
		}
		return name, drift(repo, tip, ref.Hash())
	}
	remote, merge := upstreamOf(repo, branch)
	if remote == "" {
		return "", ""
	}
	ref, err := repo.Reference(plumbing.NewRemoteReferenceName(remote, merge), true)
	if err != nil {
		return remote + "/" + merge, ""
	}
	return remote + "/" + merge, drift(repo, tip, ref.Hash())
}

func drift(repo *git.Repository, tip, upstream plumbing.Hash) string {
	if tip == upstream {
		return "up to date"
	}
	ahead, aok := notIn(repo, tip, upstream)
	behind, bok := notIn(repo, upstream, tip)
	var parts []string
	if ahead > 0 {
		parts = append(parts, fmt.Sprintf("ahead %s", plus(ahead, !aok)))
	}
	if behind > 0 {
		parts = append(parts, fmt.Sprintf("behind %s", plus(behind, !bok)))
	}
	if len(parts) == 0 {
		return "up to date"
	}
	return strings.Join(parts, ", ")
}
