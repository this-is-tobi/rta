package git

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"

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
		Description: "Every local branch, alphabetically, with the checked-out one marked, the remote branch " +
			"it tracks, and how far the two have drifted as of the last fetch; nothing here touches " +
			"the network. `gone` in Status means the tracked remote branch no longer exists, what " +
			"`git fetch --prune` leaves behind and the usual sign a merged branch can go. With `all`, " +
			"remote-tracking branches follow as `remotes/<remote>/<name>`. A detached HEAD is its own " +
			"row, and a branch checked out in another worktree is marked `worktree`: git refuses to " +
			"check it out here. What a branch tracks is read from every file of config git reads, " +
			"over MCP from the repository's own, as git.remotes reads a remote.",
		Inputs: []plugin.Field{
			pathField("repository path, or a subdirectory of one"),
			{Name: "all", Type: plugin.Bool, Config: "all", Help: "include remote-tracking branches"},
		},
		Run: runBranches,
	}
}

func runBranches(ctx context.Context, req plugin.Request) (view.View, error) {
	repo, done, verr := openRepo(ctx, req)
	if verr != nil {
		return nil, verr
	}
	defer done()

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
	pieces, err := shownConfig(ctx, req, repo)
	if verr := refusedByTheGate(err); verr != nil {
		return nil, verr
	}
	if err == nil {
		err = remoteConfigRefusal(pieces)
	}
	if err != nil {
		return nil, view.Errorf("git.branches.failed", "reading what each branch tracks: %v", err)
	}
	tracks := configuredUpstreams(pieces)
	elsewhere := checkedOutElsewhere(req, repo)

	t := view.Table{Columns: []view.Column{
		{Name: "Name"},
		{Name: "Current"},
		{Name: "Upstream"},
		{Name: "Status"},
	}, Empty: "no branches yet: a branch is made by its first commit",
		Warnings: unfollowedIncludes("git.branches", "an upstream set there is missing from this table", pieces)}
	for _, ref := range locals {
		name := ref.Name().Short()
		current := ""
		switch {
		case name == currentBranch:
			current = "yes"
		case elsewhere[name]:
			current = "worktree"
		}
		upstream, status := upstreamStatus(repo, tracks, name, ref.Hash())
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

// upstreamStatus names what a branch tracks, as tracks holds it
// (configuredUpstreams), and how the two stand.
//
// A configured upstream whose remote-tracking ref is missing is `gone`, and
// that is decided from the branch's own config section rather than through
// upstreamOf: upstreamOf falls back to a same-named remote ref precisely so
// that a never-pushed clone still reports one, and that fallback would turn a
// deleted remote branch into "no upstream" — the one case this column exists
// to make visible. The counts are what `git branch -vv` prints, from the same
// bounded walks the overview tile uses.
func upstreamStatus(repo *git.Repository, tracks map[string]upstream, branch string, tip plumbing.Hash) (
	upstream, status string,
) {
	if u := tracks[branch]; u.configured() {
		name, at := u.tracked()
		if name == "" {
			return "", ""
		}
		ref, err := repo.Reference(at, true)
		if err != nil {
			return name, "gone"
		}
		return name, drift(repo, tip, ref.Hash())
	}
	name, at := upstreamOf(repo, tracks, branch)
	if name == "" {
		return "", ""
	}
	ref, err := repo.Reference(at, true)
	if err != nil {
		return name, ""
	}
	return name, drift(repo, tip, ref.Hash())
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

// maxHeadBytes is more than a HEAD file holds: "ref: " and a branch name.
const maxHeadBytes = 4096

// checkedOutElsewhere is the branches checked out in a worktree other than
// this one, which `git branch` marks with a "+": git refuses to check one of
// them out here, and a table that showed them as free sent the reader into
// that refusal.
//
// Read from the git directories alone — worktrees/<name>/HEAD under the
// common directory, and the common directory's own HEAD when this is a
// linked worktree — so no worktree's path is read, let alone shown: a
// worktree can sit anywhere, and its path is the thing a root may not
// disclose. The common directory was judged when the repository was opened,
// and is opened here as that judgement left it (openBoundDir), so a link
// swapped in since still cannot lead out of the roots.
//
// An answer that cannot be read is no marker, never an error: nothing here
// is the reason a caller asked, and git.branches without the marker is what
// it was.
func checkedOutElsewhere(req plugin.Request, repo *git.Repository) map[string]bool {
	store, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return nil
	}
	own := store.Filesystem()
	common := commonGitDir(own)
	judged, verr := req.Confine("path", common)
	if verr != nil {
		return nil
	}
	dir, err := openBoundDir(req, judged)
	if err != nil {
		return nil
	}
	defer dir.Close()

	elsewhere := map[string]bool{}
	note := func(name string) {
		if branch := headFileBranch(dir, name); branch != "" {
			elsewhere[branch] = true
		}
	}
	linked := commonDir(own) != ""
	// A bare repository has no checkout of its own to be on a branch.
	if linked && filepath.Base(common) == gitDirName {
		note("HEAD")
	}
	entries, err := dir.ReadDir("worktrees")
	if err != nil {
		return elsewhere
	}
	for _, e := range entries {
		note(filepath.Join("worktrees", e.Name(), "HEAD"))
	}
	return elsewhere
}

// headFileBranch is the branch the HEAD file at name in dir is on, "" for a
// detached one or one that cannot be read as a HEAD file.
func headFileBranch(dir boundDir, name string) string {
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	b, err := io.ReadAll(io.LimitReader(f, maxHeadBytes))
	if err != nil {
		return ""
	}
	ref, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "ref: ")
	if !ok {
		return ""
	}
	branch, ok := strings.CutPrefix(ref, "refs/heads/")
	if !ok {
		return ""
	}
	return branch
}
