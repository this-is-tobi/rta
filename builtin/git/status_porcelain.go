package git

import (
	"github.com/go-git/go-git/v5"
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
