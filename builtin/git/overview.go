package git

import (
	"context"
	"fmt"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// detailLogLimit bounds the log section of the detailed overview — enough
// recent history to be useful, short enough to stay one screen, the same
// reasoning builtin/sys's detailTopN uses for its own top-processes section.
const detailLogLimit = 10

func overviewCapability() plugin.Capability {
	return plugin.Capability{
		ID:         "git.overview",
		Summary:    "Branch, working-tree state and the latest commit, at a glance",
		Safety:     plugin.Read,
		Idempotent: true,
		Detailed:   true,
		Description: "A one-line answer to \"what state is this repository in\": which branch is " +
			"checked out (or that HEAD is detached), whether the working tree is clean, and the " +
			"latest commit. With --detail (and on any full-page surface) it expands into the same " +
			"status, log and branches views their own capabilities return — one shape, not a " +
			"second implementation of each.",
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

	repo, verr := openRepo(ctx, req.String("path"))
	if verr != nil {
		return nil, verr
	}
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

	if wt, err := repo.Worktree(); err == nil {
		if status, err := wt.Status(); err == nil {
			if status.IsClean() {
				add("working tree", "clean")
			} else {
				add("working tree", fmt.Sprintf("%d path(s) changed", len(status)))
			}
		}
	}

	if herr == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			add("last commit", fmt.Sprintf("%s %s by %s",
				shortHash(commit.Hash), firstLine(commit.Message), commit.Author.Name))
		}
	}

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	return kv, nil
}

func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	p.AddAs("status", "status", runStatus, plugin.Read, nil)
	p.AddAs("log", "log", runLog, plugin.Read, map[string]any{"limit": detailLogLimit})
	p.AddAs("branches", "branches", runBranches, plugin.Read, nil)

	if p.Empty() {
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	return p.View(), nil
}
