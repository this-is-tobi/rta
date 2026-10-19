package git

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/this-is-tobi/rta/builtin/internal/verdict"
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
		Description: "A one-line answer to \"what state is this repository in\": a verdict (warn when a " +
			"merge or rebase is half done, paths are unmerged or the upstream is gone), which branch is " +
			"checked out (or that HEAD is detached), what it tracks as the last fetch left it, with " +
			"that fetch's age once it is a day old, whether the working tree is clean, the stash, and " +
			"the latest commit. With `detail` it expands into the same " +
			"status, log and branches views their own capabilities return — one shape, not a " +
			"second implementation of each. The working tree is read as git.status reads it, and past its " +
			"two seconds the overview is refused as git.status.timeout.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
		},
		Run: runOverview,
	}
}

// isTile reports whether this is the dashboard's run of the overview: the
// compact answer on the TUI, in a box a third of the screen wide. The page enter
// opens is the detailed one, and a terminal has its width to spend.
func isTile(req plugin.Request) bool {
	return req.Surface() == plugin.SurfaceTUI && !req.Bool("detail")
}

// runOverview answers "what state is this repository in", verdict first, then
// the facts a push or a pull depends on, and last the ones that name the
// repository rather than describe its state: the branch, then what it tracks,
// what is half done, what is changed, what is stashed, and the last commit.
// A tile is cut from the bottom.
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
	// What the verdict is made of, noted in the order it outranks: an operation
	// half done explains the unmerged paths it left, and both come before the
	// upstream that is gone.
	var op string
	var unmerged int
	var treeUnreadable, upstreamGone bool

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
		tracked := trackingOf(repo, tracks, head)
		switch {
		case tracked.gone:
			upstreamGone = true
			add("tracking", "warn — "+tracked.String())
		case tracked.upstream == "" && head.Name().IsBranch():
			// Said, where it was left out: a row that is not there reads as a
			// repository with nothing to say about it, and "am I about to push
			// to nothing" is what the row is for.
			add("tracking", "none — never pushed")
		default:
			add("tracking", tracked.String())
		}
	}
	// And before anything about files, because it changes what a file list
	// even means. An interrupted rebase is invisible in a branch name and in a
	// commit; `git status` puts it first for the same reason.
	if op = inProgress(repo); op != "" {
		add("state", "warn — "+op+" in progress")
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
			pairRenames(repo, status)
			summary := worktreeSummary(status)
			if len(ignored) > 0 {
				summary += ", " + format.CountOf(len(ignored), "ignore file") + " not applied"
			}
			// Paths a merge left undecided lead with the word the theme draws as
			// a warning: the next step is to resolve something, not to commit.
			if unmerged = countUnmerged(status); unmerged > 0 {
				summary = "warn — " + summary
			}
			add("working tree", summary)
		default:
			// Said, where it was left out: a tile with no working-tree line
			// reads as a repository with nothing to report on, which is the
			// answer for a bare one and not for a checkout whose status could
			// not be read.
			treeUnreadable = true
			add("working tree", "could not be read — "+statusFailed("git.status.failed", err).Message)
		}
	}

	if n := stashCount(repo); n > 0 {
		add("stash", strconv.Itoa(n))
	}

	if herr == nil {
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			add("last commit", lastCommit(commit, isTile(req)))
		}
	}

	if len(kv.Pairs) == 0 {
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	var found verdict.Verdict
	switch {
	case op != "":
		found.Warn(op + " in progress")
	case unmerged > 0:
		found.Warn(strconv.Itoa(unmerged) + " unmerged")
	case treeUnreadable:
		found.Warn("working tree could not be read")
	case upstreamGone:
		found.Warn("upstream gone")
	}
	kv.Pairs = slices.Insert(kv.Pairs, 0, view.Pair{Key: "status", Value: found.String()})
	return kv, nil
}

// lastCommit is the commit as a row says it: its hash, subject and author, and
// when it was made. A tile has no room for who wrote it, and what a glance wants
// of the author is that it is not a surprise.
func lastCommit(c *object.Commit, tile bool) string {
	line := fmt.Sprintf("%s %s", shortHash(c.Hash), firstLine(c.Message))
	if !tile {
		line += " by " + c.Author.Name
	}
	return line + ", " + format.Ago(c.Author.When)
}

func detailedOverview(ctx context.Context, req plugin.Request) (view.View, error) {
	p := plugin.NewPage(ctx, req)
	// The compact answer first, so the page opens with the thing the tile
	// says rather than making somebody reassemble it from three tables.
	// Page.section forces detail:false, so this is the same handler and not a
	// second implementation — and it cannot recurse.
	summary, serr := p.Run(runOverview, plugin.Read, nil)
	if serr != nil {
		p.Warn(plugin.HandlerFailure(serr, "the summary section", "page.section.failed"))
	} else {
		p.PutAs("summary", "summary", summary)
	}
	p.AddAs("status", "status", runStatus, plugin.Read, nil)
	p.AddAs("log", "log", runLog, plugin.Read, map[string]any{"limit": detailLogLimit})
	p.AddAs("branches", "branches", runBranches, plugin.Read, nil)
	// Last, because it is the section that changes least often — and present
	// at all because "which server does this reach" is the question a branch
	// name only half answers.
	p.AddAs("remotes", "remotes", runRemotes, plugin.Read, nil)

	if p.Empty() {
		// What the summary said, when it is why: every section fails for the
		// same reason outside a repository, and "nothing to report" is the
		// one answer that does not name it.
		if serr != nil {
			return nil, serr
		}
		return nil, view.Errorf("git.overview.unavailable", "nothing to report")
	}
	return p.View(), nil
}
