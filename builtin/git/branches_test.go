package git

import (
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func TestBranchesMarksTheCheckedOutOne(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("feature"), Create: true,
	}); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runBranches, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "feature")[1]; got != "yes" {
		t.Errorf("feature Current = %q, want yes", got)
	}
	if got := rowFor(t, tbl, "Name", "master")[1]; got != "" {
		t.Errorf("master Current = %q, want empty", got)
	}
}

func TestBranchesReportsDetachedHead(t *testing.T) {
	dir, repo := testRepo(t)
	hash := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{Hash: hash}); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runBranches, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "(detached at "+shortHash(hash)+")"); got[1] != "yes" {
		t.Errorf("detached row Current = %q, want yes", got[1])
	}
}
