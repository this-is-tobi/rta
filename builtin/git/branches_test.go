package git

import (
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
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

func TestBranchesReportsAGoneUpstream(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	trackRemote(t, repo, "master", "origin")

	tbl := table(t, runBranches, req(t, dir, nil))
	row := rowFor(t, tbl, "Name", "master")
	if row[2] != "origin/master" || row[3] != "gone" {
		t.Errorf("master = %v, want upstream origin/master and status gone", row[2:])
	}
}

func TestBranchesReportsDriftAgainstTheLastFetch(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	trackRemote(t, repo, "master", "origin")
	setRemoteRef(t, repo, "origin", "master", first)

	tbl := table(t, runBranches, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "master")[3]; got != "up to date" {
		t.Errorf("Status = %q, want up to date", got)
	}

	commitFile(t, repo, dir, "b.txt", "v2\n", "second")
	tbl = table(t, runBranches, req(t, dir, nil))
	if got := rowFor(t, tbl, "Name", "master")[3]; got != "ahead 1" {
		t.Errorf("Status = %q, want ahead 1", got)
	}
}

// A clone nobody has pushed from has no branch section in its config, so the
// upstream comes from the same-named remote ref — and that must not read as
// gone, since nothing was ever configured to be there.
func TestBranchesNeverPushedCloneIsNotGone(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	setRemoteRef(t, repo, "origin", "master", first)

	tbl := table(t, runBranches, req(t, dir, nil))
	row := rowFor(t, tbl, "Name", "master")
	if row[2] != "origin/master" || row[3] != "up to date" {
		t.Errorf("master = %v, want origin/master, up to date", row[2:])
	}
}

func TestBranchesAllAppendsRemoteTrackingBranches(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	setRemoteRef(t, repo, "origin", "master", first)
	setRemoteRef(t, repo, "origin", "release", first)
	head := plumbing.NewSymbolicReference(plumbing.NewRemoteReferenceName("origin", "HEAD"), plumbing.NewRemoteReferenceName("origin", "master"))
	if err := repo.Storer.SetReference(head); err != nil {
		t.Fatal(err)
	}

	without := table(t, runBranches, req(t, dir, nil))
	if without.Total != 1 {
		t.Fatalf("without all: %d rows, want the one local branch", without.Total)
	}

	with := table(t, runBranches, req(t, dir, map[string]any{"all": true}))
	names := make([]string, 0, len(with.Rows))
	for _, r := range with.Rows {
		names = append(names, r[0])
	}
	want := []string{"master", "remotes/origin/master", "remotes/origin/release"}
	if len(names) != len(want) {
		t.Fatalf("with all: names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, names[i], want[i])
		}
	}
}

func trackRemote(t *testing.T, repo *git.Repository, branch, remote string) {
	t.Helper()
	err := repo.CreateBranch(&config.Branch{
		Name:   branch,
		Remote: remote,
		Merge:  plumbing.NewBranchReferenceName(branch),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func setRemoteRef(t *testing.T, repo *git.Repository, remote, branch string, hash plumbing.Hash) {
	t.Helper()
	ref := plumbing.NewHashReference(plumbing.NewRemoteReferenceName(remote, branch), hash)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
}
