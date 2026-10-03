package git

import (
	"sort"
	"testing"

	"github.com/go-git/go-git/v5"
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

// stageMove is `git mv from to` with the content edited by edit (nothing for a
// plain move): the new name added, the old one removed, both staged.
func stageMove(t *testing.T, dir string, repo *git.Repository, from, to, content string) {
	t.Helper()
	writeFile(t, dir, to, content)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(to); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Remove(from); err != nil {
		t.Fatal(err)
	}
}

// A `git mv` is one row in `git status`, "R old -> new". go-git has no rename
// detection, so it was a D and an unrelated A, which reads as a file lost and
// another gained. Only an identical blob is paired; the working-tree half of
// the new name stays its own, and a move that also edits the file is still the
// two rows it was, since pairing it takes a similarity score this table would
// have to defend.
func TestStatusPairsAStagedMoveIntoOneRename(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "old.txt", "same content\n", "initial")
	commitFile(t, repo, dir, "keep.txt", "untouched\n", "second")
	stageMove(t, dir, repo, "old.txt", "sub/new.txt", "same content\n")
	writeFile(t, dir, "sub/new.txt", "same content\nand more\n")

	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %v, want the one rename row", tbl.Rows)
	}
	if row := tbl.Rows[0]; row[0] != "old.txt -> sub/new.txt" || row[1] != "R" || row[2] != "M" {
		t.Errorf("row = %q, want `old.txt -> sub/new.txt` R M, as git prints it", row)
	}
	if tbl.Total != 1 {
		t.Errorf("total = %d, want 1", tbl.Total)
	}
}

func TestStatusKeepsAMoveWithEditsAsADeleteAndAnAdd(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "old.txt", "some content\n", "initial")
	stageMove(t, dir, repo, "old.txt", "new.txt", "some other content\n")

	tbl := table(t, runStatus, req(t, dir, nil))
	if got := rowFor(t, tbl, "Path", "old.txt"); got[1] != "D" {
		t.Errorf("old.txt = %q, want D", got)
	}
	if got := rowFor(t, tbl, "Path", "new.txt"); got[1] != "A" {
		t.Errorf("new.txt = %q, want A", got)
	}
}

// Two files of one content, both moved: each pairs once, the one with the
// same file name first, as git prefers it.
func TestStatusPairsEachMovedCopyOnce(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a/x.txt", "twin\n", "first")
	commitFile(t, repo, dir, "b/y.txt", "twin\n", "second")
	stageMove(t, dir, repo, "a/x.txt", "c/y.txt", "twin\n")
	stageMove(t, dir, repo, "b/y.txt", "d/x.txt", "twin\n")

	tbl := table(t, runStatus, req(t, dir, nil))
	got := map[string]bool{}
	for _, row := range tbl.Rows {
		got[row[0]+"|"+row[1]] = true
	}
	for _, want := range []string{"b/y.txt -> c/y.txt|R", "a/x.txt -> d/x.txt|R"} {
		if !got[want] {
			t.Errorf("rows = %v, want %q", tbl.Rows, want)
		}
	}
}
