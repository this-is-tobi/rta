package git

import (
	"context"
	"errors"
	"fmt"

	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// detailLogLimit bounds the log section of the detailed overview — enough
// recent history to be useful, short enough to stay one screen, the same
// reasoning builtin/sys's detailTopN uses for its own top-processes section.
const detailLogLimit = 10

func overviewCapability() plugin.Capability {
	return plugin.Capability{
		ID:           "git.overview",
		Summary:      "Branch, working-tree state and the latest commit, at a glance",
		Safety:       plugin.Read,
		HostSpecific: true,
		Idempotent:   true,
		Detailed:     true,
		Description: "A one-line answer to \"what state is this repository in\": which branch is " +
			"checked out (or that HEAD is detached), whether the working tree is clean, and the " +
			"latest commit. With `detail` (and on any full-page surface) it expands into the same " +
			"status, log and branches views their own capabilities return — one shape, not a " +
			"second implementation of each. The working tree is read as git.status reads it, and past its " +
			"two seconds the overview is refused as git.status.timeout.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runOverview,
	}
}

func runOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	if req.Bool("detail") {
		return detailedOverview(ctx, req)
	}

	repo, done, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()
	kv := view.KeyValue{}
	add := func(key, value string) {
		if value != "" {
			kv.Pairs = append(kv.Pairs, view.Pair{Key: key, Value: value})
		}
	}

	head, herr := repo.Head()
	switch {
	case herr != nil:
		add("branch", "no commits yet")
	case head.Name().IsBranch():
		add("branch", head.Name().Short())
	default:
		add("branch", "detached at "+shortHash(head.Hash()))
	}

	// **Second, because it is the first thing that changes what to do next.**
	// A tile that says "main, clean, last commit an hour ago" and does not say
	// "4 ahead of origin/main" has answered the easy half: the reason to look
	// at a repository's state is almost always to decide whether to push,
	// pull, or carry on.
	if herr == nil {
		tracks, err := branchUpstreams(ctx, req, repo)
		if verr := refusedByTheGate(err); verr != nil {
			return nil, verr
		}
		if err != nil {
			return nil, view.Errorf("git.overview.failed", "reading what the branch tracks: %v", err)
		}
		add("tracking", trackingOf(repo, tracks, head).String())
	}
	// And before anything about files, because it changes what a file list
	// even means. An interrupted rebase is invisible in a branch name and in a
	// commit; `git status` puts it first for the same reason.
	if op := inProgress(repo); op != "" {
		add("state", op+" in progress")
	}

	if wt, err := repo.Worktree(); err == nil {
		status, ignored, err := worktreeStatus(ctx, statusDeadline(ctx), repo, wt, req)
		var refused *view.Error
		switch {
		case errors.As(err, &refused):
			// Refused as git.status refuses it, past the call's time: the
			// part of the tree it had read would summarise as a cleaner one.
			return nil, refused
		case err == nil:
			summary := worktreeSummary(status)
			if len(ignored) > 0 {
				summary += ", " + format.CountOf(len(ignored), "ignore file") + " not applied"
			}
			add("working tree", summary)
		default:
			// Said, where it was left out: a tile with no working-tree line
			// reads as a repository with nothing to report on, which is the
			// answer for a bare one and not for a checkout whose status could
			// not be read.
			add("working tree", "unreadable — "+statusFailed("git.status.failed", err).Message)
		}
	}

	if herr == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			add("last commit", fmt.Sprintf("%s %s by %s, %s",
				shortHash(commit.Hash), firstLine(commit.Message), commit.Author.Name,
				format.Ago(commit.Author.When)))
		}
	}

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	return kv, nil
}

func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	// The compact answer first, so the page opens with the thing the tile
	// says rather than making somebody reassemble it from three tables.
	// Page.section forces detail:false, so this is the same handler and not a
	// second implementation — and it cannot recurse.
	p.AddAs("summary", "summary", runOverview, plugin.Read, nil)
	p.AddAs("status", "status", runStatus, plugin.Read, nil)
	p.AddAs("log", "log", runLog, plugin.Read, map[string]any{"limit": detailLogLimit})
	p.AddAs("branches", "branches", runBranches, plugin.Read, nil)
	// Last, because it is the section that changes least often — and present
	// at all because "which server does this reach" is the question a branch
	// name only half answers.
	p.AddAs("remotes", "remotes", runRemotes, plugin.Read, nil)

	if p.Empty() {
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	return p.View(), nil
}
