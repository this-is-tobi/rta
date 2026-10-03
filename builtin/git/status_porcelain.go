package git

import (
	"path"
	"sort"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// markIntentToAdd tells a path `git add -N` recorded as git tells it: " A",
// not yet in the index with any content, and the whole file new on the
// working-tree side. go-git compares the index entry, which holds the empty
// blob a path recorded this way is given, with HEAD and the disk, and reports
// a staged add with the working copy modified: "AM", which says the file is
// staged and its content is part of the next commit, which it is not.
func markIntentToAdd(idx *index.Index, status git.Status) {
	for _, e := range idx.Entries {
		s, ok := status[e.Name]
		if !e.IntentToAdd || !ok || s.Worktree == git.Deleted {
			continue
		}
		status[e.Name] = &git.FileStatus{Staging: git.Unmodified, Worktree: git.Added}
	}
}

// pairRenames folds a staged delete and a staged add of the same content into
// the one row `git status` shows, "R", with the old name in Extra, as git
// pairs a rename. go-git has no rename detection, so a `git mv` was two rows,
// a D and an A, and read as a file removed and an unrelated one added.
//
// **Only an exact rename is paired.** git pairs a file that was renamed and
// edited too when the two are at least half alike, which takes a diff of
// every candidate pair; what is staged as an identical blob is the common
// case (`git mv`), costs a lookup by hash, and is never wrong, where a
// similarity score is a judgement this table would then have to defend. A
// rename with edits stays a D and an A, as it was.
//
// A deleted path is paired only while it is still gone from the working tree
// (nothing at the old name that `git status` would also list), and each side
// pairs once: with several candidates of one content, the one with the same
// file name wins, as git prefers it, and then the first in path order.
func pairRenames(repo *git.Repository, status git.Status) {
	var added, deleted []string
	for p, s := range status {
		switch {
		case s.Staging == git.Added && s.Worktree != git.Untracked:
			added = append(added, p)
		case s.Staging == git.Deleted && s.Worktree == git.Unmodified:
			deleted = append(deleted, p)
		}
	}
	if len(added) == 0 || len(deleted) == 0 {
		return
	}
	idx, err := repo.Storer.Index()
	if err != nil {
		return
	}
	head, entryOf := headFilesOf(repo), indexLookup(idx)
	sort.Strings(added)
	sort.Strings(deleted)
	gone := map[string][]string{}
	for _, p := range deleted {
		if e, _ := head.entry(p); e != nil && (e.Mode == filemode.Regular || e.Mode == filemode.Executable) {
			gone[e.Hash.String()] = append(gone[e.Hash.String()], p)
		}
	}
	for _, p := range added {
		e := entryOf(p)
		if e == nil || (e.Mode != filemode.Regular && e.Mode != filemode.Executable) {
			continue
		}
		candidates := gone[e.Hash.String()]
		if len(candidates) == 0 {
			continue
		}
		pick := 0
		for i, c := range candidates {
			if path.Base(c) == path.Base(p) {
				pick = i
				break
			}
		}
		old := candidates[pick]
		gone[e.Hash.String()] = append(candidates[:pick:pick], candidates[pick+1:]...)
		status[p] = &git.FileStatus{Staging: git.Renamed, Worktree: status[p].Worktree, Extra: old}
		delete(status, old)
	}
}

// statusPath is how a row names a path: as `git status --porcelain` does, the
// old name first for a rename.
func statusPath(p string, s *git.FileStatus) string {
	if s.Staging == git.Renamed && s.Extra != "" {
		return s.Extra + " -> " + p
	}
	return p
}
