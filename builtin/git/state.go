package git

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"
)

// The facts a person actually opens `git status` for, and that a hash and a
// commit message do not carry.
//
// The overview used to answer three questions — which branch, is the tree
// clean, what was the last commit — and stopped exactly where the interesting
// ones start. *Am I about to push something?* *Am I behind?* *Is this
// repository mid-rebase?* Those are what decide the next command, and all
// three were invisible until somebody dropped to a shell, which is the thing a
// dashboard tile exists to save.

// walkLimit bounds both halves of the ahead/behind count.
//
// A divergence measured against a remote-tracking ref is a walk over history,
// and history has no bound. Five hundred is far past any branch a person is
// deciding about — "500+ ahead" and "1200 ahead" lead to the same next
// command — and it keeps a tile that redraws on a timer from walking a
// hundred thousand commits on a repository somebody left open.
const walkLimit = 500

// tracking is where a branch pushes and how far it has drifted from it.
type tracking struct {
	upstream string // "origin/main", or empty when the branch tracks nothing
	ahead    int
	behind   int
	// capped says the walk stopped at walkLimit, so the counts are floors
	// rather than answers.
	capped bool
}

// String renders the drift the way `git status` says it, and says nothing at
// all when there is nothing to say — a branch level with its upstream is the
// ordinary case and does not need a line about it.
func (t tracking) String() string {
	if t.upstream == "" {
		return ""
	}
	var parts []string
	if t.ahead > 0 {
		parts = append(parts, fmt.Sprintf("%s ahead", plus(t.ahead, t.capped)))
	}
	if t.behind > 0 {
		parts = append(parts, fmt.Sprintf("%s behind", plus(t.behind, t.capped)))
	}
	if len(parts) == 0 {
		return t.upstream + " (up to date)"
	}
	return t.upstream + " (" + strings.Join(parts, ", ") + ")"
}

func plus(n int, capped bool) string {
	if capped && n >= walkLimit {
		return fmt.Sprintf("%d+", n)
	}
	return fmt.Sprintf("%d", n)
}

// trackingOf reports what the checked-out branch tracks and how far it has
// drifted.
//
// **From the remote-tracking ref, and never from the network.** `origin/main`
// is what the last fetch left behind, so "3 behind" means three commits behind
// what this machine last saw — which is what `git status` says too, and is the
// only answer available to something that must not open a socket to draw a
// tile. A repository that has not fetched in a week reports against a week-old
// picture, and that is the honest reading of a local repository's state.
func trackingOf(repo *git.Repository, head *plumbing.Reference) tracking {
	if head == nil || !head.Name().IsBranch() {
		return tracking{}
	}
	branch := head.Name().Short()
	remote, merge := upstreamOf(repo, branch)
	if remote == "" {
		return tracking{}
	}
	name := remote + "/" + merge
	ref, err := repo.Reference(plumbing.NewRemoteReferenceName(remote, merge), true)
	if err != nil {
		// Configured but never fetched: naming it is still the useful answer,
		// since "where would this push" is half the question.
		return tracking{upstream: name}
	}
	ahead, aok := notIn(repo, head.Hash(), ref.Hash())
	behind, bok := notIn(repo, ref.Hash(), head.Hash())
	return tracking{upstream: name, ahead: ahead, behind: behind, capped: !aok || !bok}
}

// upstreamOf reads branch.<name>.remote and branch.<name>.merge, falling back
// to a remote-tracking ref of the same name.
//
// The fallback is for the repository somebody cloned and never pushed from:
// git writes the branch section on the first push, so before that `main` has
// no configured upstream while `origin/main` sits right there. Reporting
// nothing would be technically correct and useless.
func upstreamOf(repo *git.Repository, branch string) (remote, merge string) {
	if b, err := repo.Branch(branch); err == nil && b.Remote != "" {
		name := branch
		if b.Merge != "" {
			name = b.Merge.Short()
		}
		return b.Remote, name
	}
	// From the remote-tracking refs themselves rather than from the configured
	// remotes: what makes an upstream *reportable* is that this machine has
	// actually seen the branch there, and `refs/remotes/origin/main` is that
	// evidence. Going through the remote list would answer from configuration
	// and then still have to check the ref.
	refs, err := repo.References()
	if err != nil {
		return "", ""
	}
	var found []string
	suffix := "/" + branch
	_ = refs.ForEach(func(r *plumbing.Reference) error {
		name := r.Name()
		if !name.IsRemote() {
			return nil
		}
		if short := name.Short(); strings.HasSuffix(short, suffix) {
			found = append(found, strings.TrimSuffix(short, suffix))
		}
		return nil
	})
	if len(found) == 0 {
		return "", ""
	}
	// origin first, then whatever else there is in a stable order: a
	// repository with two remotes must not report a different upstream on
	// every redraw.
	sort.Slice(found, func(i, j int) bool {
		if (found[i] == "origin") != (found[j] == "origin") {
			return found[i] == "origin"
		}
		return found[i] < found[j]
	})
	return found[0], branch
}

// notIn counts the commits reachable from `tip` and not from `other`, and
// reports whether it finished.
//
// Two bounded walks rather than a merge base: `MergeBase` is itself a walk
// with no bound, and the answer wanted here is a small number or the word
// "many". The reachable set is collected first so that a commit on both sides
// is excluded however many merges it arrives through — counting by walking
// until the base is met gets criss-cross histories wrong, quietly.
func notIn(repo *git.Repository, tip, other plumbing.Hash) (int, bool) {
	common, ok := reachable(repo, other, walkLimit*2)
	n := 0
	seen := make(map[plumbing.Hash]bool, walkLimit)
	queue := []plumbing.Hash{tip}
	for len(queue) > 0 {
		h := queue[0]
		queue = queue[1:]
		if seen[h] || common[h] {
			continue
		}
		seen[h] = true
		n++
		if n >= walkLimit {
			return n, false
		}
		c, err := repo.CommitObject(h)
		if err != nil {
			continue
		}
		queue = append(queue, c.ParentHashes...)
	}
	return n, ok
}

// reachable collects the commits reachable from h, up to limit.
func reachable(repo *git.Repository, h plumbing.Hash, limit int) (map[plumbing.Hash]bool, bool) {
	seen := make(map[plumbing.Hash]bool, limit/8)
	queue := []plumbing.Hash{h}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		if len(seen) >= limit {
			return seen, false
		}
		c, err := repo.CommitObject(cur)
		if err != nil {
			continue
		}
		queue = append(queue, c.ParentHashes...)
	}
	return seen, true
}

// operations maps the marker git leaves in the repository directory to the
// operation it is in the middle of.
//
// Ordered, because more than one can be present: a rebase that stopped on a
// conflict has both a rebase directory and MERGE_HEAD, and the useful word is
// the outer one — "continue the rebase", not "finish the merge".
var operations = []struct{ marker, name string }{
	{"rebase-merge", "rebase"},
	{"rebase-apply", "rebase"},
	{"CHERRY_PICK_HEAD", "cherry-pick"},
	{"REVERT_HEAD", "revert"},
	{"MERGE_HEAD", "merge"},
	{"BISECT_LOG", "bisect"},
}

// inProgress names the operation this repository is in the middle of, if any.
//
// It is the fact most worth surfacing and the one least visible: an
// interrupted rebase changes what every other command means, and nothing about
// a branch name, a commit or a file list says it is happening. `git status`
// puts it first for that reason.
//
// Read from the repository directory through go-git's own filesystem handle,
// which is how it stays correct for a worktree or a submodule — where the
// markers are not under the `.git` beside the checkout — and absent for a
// repository cloned into memory, which has no directory and no operation
// either.
func inProgress(repo *git.Repository) string {
	fs, ok := repo.Storer.(*filesystem.Storage)
	if !ok {
		return ""
	}
	dir := fs.Filesystem()
	for _, op := range operations {
		if _, err := dir.Stat(op.marker); err == nil {
			return op.name
		}
	}
	return ""
}

// worktreeSummary counts a status by what a person would do about it: what is
// staged and ready to commit, what is changed and not staged, what is not
// tracked at all.
//
// "7 path(s) changed" was one number for three different situations. Three
// counts fit in the same line and answer the question the number was standing
// in for — whether there is anything to commit, anything to add, or only
// build output nobody has ignored yet.
func worktreeSummary(status git.Status) string {
	staged, changed, untracked := 0, 0, 0
	for _, s := range status {
		switch {
		case s.Worktree == git.Untracked && s.Staging == git.Untracked:
			untracked++
			continue
		case s.Staging != git.Unmodified:
			staged++
		}
		if s.Worktree != git.Unmodified && s.Worktree != git.Untracked {
			changed++
		}
	}
	if staged+changed+untracked == 0 {
		return "clean"
	}
	var parts []string
	if staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", staged))
	}
	if changed > 0 {
		parts = append(parts, fmt.Sprintf("%d modified", changed))
	}
	if untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", untracked))
	}
	return strings.Join(parts, ", ")
}
