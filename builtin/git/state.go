package git

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/storage/filesystem"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
	// gone is an upstream configured for the branch that this repository has
	// no remote-tracking ref for, so nothing was counted.
	gone bool
}

// String renders the drift the way `git status` says it, and says nothing at
// all when there is nothing to say — a branch level with its upstream is the
// ordinary case and does not need a line about it.
func (t tracking) String() string {
	switch {
	case t.upstream == "":
		return ""
	case t.gone:
		return t.upstream + " (gone)"
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
func trackingOf(repo *git.Repository, tracks map[string]upstream, head *plumbing.Reference) tracking {
	if head == nil || !head.Name().IsBranch() {
		return tracking{}
	}
	name, at := upstreamOf(repo, tracks, head.Name().Short())
	if name == "" {
		return tracking{}
	}
	ref, err := repo.Reference(at, true)
	if err != nil {
		// Configured but with no ref here, never fetched or pruned since:
		// naming it is still the useful answer, since "where would this
		// push" is half the question, and it is gone, as git.branches and
		// `git status` say. Nothing was compared, and a tracking with no
		// counts read as "up to date".
		return tracking{upstream: name, gone: true}
	}
	ahead, aok := notIn(repo, head.Hash(), ref.Hash())
	behind, bok := notIn(repo, ref.Hash(), head.Hash())
	return tracking{upstream: name, ahead: ahead, behind: behind, capped: !aok || !bok}
}

// upstreamOf reads branch.<name>.remote and branch.<name>.merge, as tracks
// holds them (configuredUpstreams), falling back to a remote-tracking ref of
// the same name: the upstream as git names it, "" where there is none, and
// the ref it is (upstream.tracked).
//
// The fallback is for the repository somebody cloned and never pushed from:
// git writes the branch section on the first push, so before that `main` has
// no configured upstream while `origin/main` sits right there. Reporting
// nothing would be technically correct and useless.
func upstreamOf(repo *git.Repository, tracks map[string]upstream, branch string) (name string,
	ref plumbing.ReferenceName,
) {
	if u := tracks[branch]; u.configured() {
		return u.tracked()
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
	return found[0] + "/" + branch, plumbing.NewRemoteReferenceName(found[0], branch)
}

// upstream is what a branch is configured to track: the remote,
// branch.<name>.remote, the branch there, branch.<name>.merge, and the
// remote's fetch refspecs, remote.<remote>.fetch in the order git reads them,
// which say where a fetch keeps that branch here.
type upstream struct {
	remote, merge string
	fetch         []string
}

// configured reports whether u is an upstream at all, as git's set_merge
// reads one: a remote and a branch there to merge, both set.
//
// **A remote alone is no upstream.** `git rev-parse @{upstream}` says "no
// upstream configured" of a branch whose section names a remote and nothing
// to merge, and `git status` names none. This took the branch's own name for
// the one it merges, and said such a branch tracked origin/<itself>, gone
// where there is no such ref, or, with a remote of ".", itself, up to date.
func (u upstream) configured() bool { return u.remote != "" && u.merge != "" }

// tracked is the ref u, a configured upstream, is, and its name as git names
// it, the ref's short name; "" for both where git finds none.
//
// **Where the remote's fetch keeps the branch, as git's remote_find_tracking
// finds it** (trackingRef). This took refs/remotes/<remote>/<branch> whatever
// the remote's refspec said: a remote fetched into refs/remotes/mirror/ was
// tracked at a ref no fetch writes, and said to be gone, and a branch whose
// remote fetches nothing of it, or is not configured at all, of which git
// finds no upstream ("not stored as a remote-tracking branch"), was said to
// track one.
//
// **A remote of "." is the repository itself.** `git branch --track feature
// main` sets it, and the branch then tracks the ref it merges here, named as
// that ref's own short name: git counts feature against main. This looked for
// a remote named "." and said "./main" was gone.
func (u upstream) tracked() (name string, ref plumbing.ReferenceName) {
	merge := plumbing.ReferenceName(u.merge)
	if u.remote != "." {
		dst, ok := trackingRef(u.fetch, u.merge)
		if !ok {
			return "", ""
		}
		ref = plumbing.ReferenceName(dst)
		return ref.Short(), ref
	}
	if !strings.HasPrefix(u.merge, "refs/") {
		merge = plumbing.NewBranchReferenceName(u.merge)
	}
	return merge.Short(), merge
}

// trackingRef is where a fetch through refspecs, a remote's fetch refspecs,
// keeps src, a ref of the remote's, as git's remote_find_tracking finds it
// (query_refspecs): the destination of the first refspec whose source is src,
// or a pattern src matches, the part of src its * matched put in place of the
// destination's; ok is false where none is, and where a negative refspec
// excludes src.
//
// A refspec with no destination keeps nothing, and is passed over; one
// whose destination is written empty, `refs/heads/main:`, is a match that
// keeps nothing, and where it is the first to match there is no upstream, as
// git finds none. One git refuses to fetch with is passed over
// (parseRefspec). And where src names the remote's ref by a short name, as
// branch.<name>.merge = main does, it matches no source, which are full
// names, and git finds no upstream for it.
//
// **A negative refspec is matched as git matches it**, which is not the way
// round a reader expects: git takes src back through each positive refspec,
// matching a pattern's destination, and asks whether a negative refspec
// excludes what that gives (query_matches_negative_refspec). So
// ^refs/heads/main excludes main where a refspec names it exactly, or maps
// refs/heads/* to refs/heads/*, and not beside the usual
// +refs/heads/*:refs/remotes/origin/*, which git 2.50 tracks main through.
func trackingRef(refspecs []string, src string) (dst string, ok bool) {
	var positive, negative []refspec
	for _, s := range refspecs {
		if r, valid := parseRefspec(s); valid && r.negative {
			negative = append(negative, r)
		} else if valid {
			positive = append(positive, r)
		}
	}
	for _, r := range positive {
		var reversed string
		switch {
		case r.pattern:
			key := r.dst
			if key == "" {
				key = r.src
			}
			if back, matched := matchPattern(key, src, r.src); matched {
				reversed = back
			} else {
				continue
			}
		case src == r.src:
			reversed = r.src
		default:
			continue
		}
		for _, n := range negative {
			if n.pattern {
				if _, matched := matchPattern(n.src, reversed, ""); matched {
					return "", false
				}
			} else if n.src == reversed {
				return "", false
			}
		}
	}
	for _, r := range positive {
		if !r.stores {
			continue
		}
		if !r.pattern {
			if src == r.src {
				return r.dst, r.dst != ""
			}
			continue
		}
		if mapped, matched := matchPattern(r.src, src, r.dst); matched {
			return mapped, true
		}
	}
	return "", false
}

// refspec is one of a remote's fetch refspecs, read as git's parse_refspec
// reads one to fetch with: its source and its destination, "" where it has
// none, whether it names one at all, which `refs/heads/main:` does and
// `refs/heads/main` does not, whether both are patterns, and whether it is a
// negative one, which has a source alone.
type refspec struct {
	src, dst                  string
	stores, pattern, negative bool
}

// parseRefspec is s read as a fetch refspec, as git's parse_refspec reads
// one; valid is false where git refuses it ("invalid refspec"): a negative
// one with a destination, or empty, or naming an object by its hash; a * on
// one side alone, or none on the source of one with no destination, but for
// a negative one; and a side that is not a ref name as git reads one in a
// refspec (refnameInRefspec). A + forces, and a ^ after it is no negative
// one but a ref name with a ^ in it, which none is. @ is HEAD.
//
// **Read in full, as git reads it, since git refuses to run with one it
// cannot.** A * on one side alone, or two on one, was taken for a pattern,
// and refs/heads/*:refs/remotes/o/** tracked main at o/main*, where git
// refuses the repository.
func parseRefspec(s string) (r refspec, valid bool) {
	switch {
	case strings.HasPrefix(s, "+"):
		s = s[1:]
	case strings.HasPrefix(s, "^"):
		r.negative, s = true, s[1:]
	}
	i := strings.LastIndex(s, ":")
	r.stores = i >= 0
	if r.stores {
		r.src, r.dst = s[:i], s[i+1:]
	} else {
		r.src = s
	}
	if r.negative && r.stores {
		return refspec{}, false
	}
	r.pattern = strings.Contains(r.dst, "*")
	if strings.Contains(r.src, "*") {
		if (r.stores && !r.pattern) || (!r.stores && !r.negative) {
			return refspec{}, false
		}
		r.pattern = true
	} else if r.pattern {
		return refspec{}, false
	}
	if r.negative {
		return r, r.src != "" && !isObjectName(r.src) && refnameInRefspec(r.src, r.pattern)
	}
	if r.src == "@" {
		r.src = "HEAD"
	}
	return r, (r.src == "" || refnameInRefspec(r.src, r.pattern)) && (r.dst == "" || refnameInRefspec(r.dst, r.pattern))
}

// isObjectName reports whether s is an object's name in full, as a refspec
// may name one, which a negative refspec may not.
func isObjectName(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// refnameInRefspec reports whether name is a ref name as git's
// check_refname_format reads one on a side of a refspec: one level of it
// allowed, and one * where it is a pattern. No part of it empty, starting
// with a dot or ending in .lock; no .., @{, control character, space, or
// any of : ? [ \ ^ ~; not @ alone, and not ending with a dot.
func refnameInRefspec(name string, pattern bool) bool {
	if name == "@" || strings.HasSuffix(name, ".") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part[0] == '.' || strings.HasSuffix(part, ".lock") {
			return false
		}
		var last byte
		for i := 0; i < len(part); i++ {
			switch c := part[i]; {
			case c < 0x20 || c == 0x7f || strings.IndexByte(" :?[\\^~", c) >= 0,
				c == '.' && last == '.', c == '{' && last == '@':
				return false
			case c == '*' && !pattern:
				return false
			case c == '*':
				pattern = false
			}
			last = part[i]
		}
	}
	return true
}

// matchPattern is whether name matches key, a pattern with one *, as git's
// match_name_with_pattern matches it: what comes before the * and what comes
// after it are name's start and end, with nothing of them shared; and value,
// where there is one, with its * replaced by what the * of key matched.
func matchPattern(key, name, value string) (mapped string, matched bool) {
	before, after, found := strings.Cut(key, "*")
	if !found || len(name) < len(before)+len(after) || !strings.HasPrefix(name, before) ||
		!strings.HasSuffix(name, after) {
		return "", false
	}
	if value == "" {
		return "", true
	}
	head, tail, _ := strings.Cut(value, "*")
	return head + name[len(before):len(name)-len(after)] + tail, true
}

// configuredUpstreams is what each branch tracks as pieces set it, read as
// git's remote.c reads it: the last branch.<name>.remote, and the first
// branch.<name>.merge, the one git names as the upstream of a branch that
// merges several; and the fetch refspecs of each remote, every one set, which
// say where a fetch keeps what it fetches (upstream.tracked).
//
// **From every file git reads, not the repository's own.** go-git's Branch
// reads .git/config alone, and the last merge in it, so an upstream set in a
// file an include names, or in ~/.gitconfig, was missing from git.branches
// and the overview, where git's status names it. The pieces are the ones the
// caller may be shown (shownConfig), as git.remotes reads its remotes.
func configuredUpstreams(pieces []scopedConfig) map[string]upstream {
	out := map[string]upstream{}
	fetch := map[string][]string{}
	for _, p := range pieces {
		if p.config.Raw.HasSection("remote") {
			for _, sub := range p.config.Raw.Section("remote").Subsections {
				for _, o := range sub.Options {
					if o.IsKey("fetch") {
						fetch[sub.Name] = append(fetch[sub.Name], o.Value)
					}
				}
			}
		}
		if !p.config.Raw.HasSection("branch") {
			continue
		}
		for _, sub := range p.config.Raw.Section("branch").Subsections {
			u := out[sub.Name]
			for _, o := range sub.Options {
				switch {
				case o.IsKey("remote"):
					u.remote = o.Value
				case o.IsKey("merge") && u.merge == "":
					u.merge = o.Value
				}
			}
			out[sub.Name] = u
		}
	}
	for name, u := range out {
		u.fetch = fetch[u.remote]
		out[name] = u
	}
	return out
}

// branchUpstreams is what each branch of repo tracks, as configuredUpstreams
// reads it from the config req may be shown.
func branchUpstreams(ctx context.Context, req plugin.Request, repo *git.Repository) (map[string]upstream, error) {
	pieces, err := shownConfig(ctx, req, repo)
	if err != nil {
		return nil, err
	}
	return configuredUpstreams(pieces), nil
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
			// **A parent that is not in the object store is a boundary, not
			// the end of the history.** A shallow clone's boundary commit
			// has real parents that were never fetched, and dropping them
			// silently stopped the walk there while still reporting a
			// finished count — so "3 ahead" was stated about a branch that
			// may be three hundred ahead. `capped` already exists for
			// exactly this and only the walk limit ever set it.
			ok = false
			continue
		}
		queue = append(queue, c.ParentHashes...)
	}
	return n, ok
}

// reachable collects the commits reachable from h, up to limit.
func reachable(repo *git.Repository, h plumbing.Hash, limit int) (map[plumbing.Hash]bool, bool) {
	seen := make(map[plumbing.Hash]bool, limit/8)
	complete := true
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
			// Same boundary, same consequence: a common-set this could not
			// finish collecting makes the count on the other side an
			// over-count, and the caller has to be told it is a floor.
			complete = false
			continue
		}
		queue = append(queue, c.ParentHashes...)
	}
	return seen, complete
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
