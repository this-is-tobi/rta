package git

import (
	"sort"
	"testing"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
)

// emptyBlob is the object `git add -N` records for a path it has not read yet.
var emptyBlob = plumbing.NewHash("e69de29bb2d1d6434b8b29ae775ad8c2e48c5391")

// `git add -N` records that a path will be added without adding its content,
// and `git status` shows it as " A": new, and not staged. go-git read the
// empty blob the index holds as the staged content of the file and the disk
// as a change to it, so the row said "AM", which says the whole file is part
// of the next commit.
func TestStatusShowsAnIntentToAddAsGitDoes(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "one\n", "initial")
	writeFile(t, dir, "later.txt", "not staged yet\n")

	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	idx.Version = 3
	idx.Entries = append(idx.Entries, &index.Entry{
		Name: "later.txt", Hash: emptyBlob, Mode: filemode.Regular, IntentToAdd: true,
	})
	sort.Slice(idx.Entries, func(i, j int) bool { return idx.Entries[i].Name < idx.Entries[j].Name })
	if err := repo.Storer.SetIndex(idx); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runStatus, req(t, dir, nil))
	if row := rowFor(t, tbl, "Path", "later.txt"); row[1] != " " || row[2] != "A" {
		t.Errorf("later.txt = %q %q, want staged blank and worktree A, as git prints \" A\"", row[1], row[2])
	}
}
