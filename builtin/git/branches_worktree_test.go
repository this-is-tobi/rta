package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// linkedLayout is what `git worktree add ../linked feature` leaves: an admin
// directory under the main .git, holding the linked worktree's own HEAD, and
// a .git file in the linked checkout pointing back at it.
func linkedLayout(t *testing.T, root, branch string) (main, linked string) {
	t.Helper()
	main = filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInit(main, false)
	if err != nil {
		t.Fatal(err)
	}
	head := commitFile(t, repo, main, "a.txt", "a\n", "on the main checkout")
	for _, name := range []string{branch, "spare"} {
		if err := repo.Storer.SetReference(plumbing.NewHashReference(plumbing.NewBranchReferenceName(name), head)); err != nil {
			t.Fatal(err)
		}
	}
	linked = filepath.Join(root, "linked")
	admin := filepath.Join(main, ".git", "worktrees", "linked")
	writeFile(t, admin, "HEAD", "ref: refs/heads/"+branch+"\n")
	writeFile(t, admin, "commondir", "../..\n")
	writeFile(t, admin, "gitdir", filepath.Join(linked, ".git")+"\n")
	writeFile(t, linked, ".git", "gitdir: "+admin+"\n")
	return main, linked
}

// `git branch` marks a branch checked out in another worktree with a "+",
// because git refuses to check it out here; the table showed it as free. Read
// from the git directories alone, from the main checkout and from the linked
// one, and over MCP under a root holding both without naming a path.
func TestBranchesMarksABranchCheckedOutInAnotherWorktree(t *testing.T) {
	root := t.TempDir()
	main, linked := linkedLayout(t, root, "feature")

	for name, r := range map[string]struct{ at, branch, other string }{
		"main":   {main, "master", "feature"},
		"linked": {linked, "feature", "master"},
	} {
		tbl := table(t, runBranches, req(t, r.at, nil))
		if got := rowFor(t, tbl, "Name", r.branch)[1]; got != "yes" {
			t.Errorf("from the %s checkout, %s Current = %q, want yes", name, r.branch, got)
		}
		if got := rowFor(t, tbl, "Name", r.other)[1]; got != "worktree" {
			t.Errorf("from the %s checkout, %s Current = %q, want worktree", name, r.other, got)
		}
		if got := rowFor(t, tbl, "Name", "spare")[1]; got != "" {
			t.Errorf("from the %s checkout, an unchecked branch Current = %q, want empty", name, got)
		}
		for _, row := range tbl.Rows {
			if strings.Contains(strings.Join(row, " "), root) {
				t.Errorf("from the %s checkout, a row names a path: %v", name, row)
			}
		}
	}

	tbl := table(t, runBranches, guarded(t, root, main))
	if got := rowFor(t, tbl, "Name", "feature")[1]; got != "worktree" {
		t.Errorf("under a root holding both, feature Current = %q, want worktree", got)
	}
}

// A root drawn around the main checkout alone cannot reach the worktree that
// sits beside it, and does not need to: the marker comes from its admin
// directory under the main .git, so nothing outside the root is read and no
// path of it is shown.
func TestBranchesMarksAWorktreeBesideARootWithoutReadingIt(t *testing.T) {
	root := t.TempDir()
	main, _ := linkedLayout(t, root, "feature")

	tbl := table(t, runBranches, guarded(t, main, main))
	if got := rowFor(t, tbl, "Name", "feature")[1]; got != "worktree" {
		t.Errorf("feature Current = %q, want worktree: its admin directory is inside the main .git", got)
	}
}
