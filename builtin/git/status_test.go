package git

import (
	"context"
	"testing"
)

func TestStatusReportsCleanRepoAsEmpty(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "hello\n", "initial")

	tbl := table(t, runStatus, req(t, dir, nil))
	if len(tbl.Rows) != 0 {
		t.Errorf("rows = %v, want none — nothing changed since the last commit", tbl.Rows)
	}
}

func TestStatusDistinguishesUntrackedModifiedAndStaged(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "tracked.txt", "v1\n", "initial")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}

	// Untracked: never added.
	writeFile(t, dir, "new.txt", "brand new\n")

	// Modified, unstaged: on disk but not re-added.
	writeFile(t, dir, "tracked.txt", "v2\n")

	// Staged: added but not committed.
	writeFile(t, dir, "staged.txt", "staged content\n")
	if _, err := wt.Add("staged.txt"); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runStatus, req(t, dir, nil))
	if got := rowFor(t, tbl, "Path", "new.txt"); got[2] != "?" {
		t.Errorf("new.txt worktree status = %q, want untracked (?)", got[2])
	}
	if got := rowFor(t, tbl, "Path", "tracked.txt"); got[2] != "M" {
		t.Errorf("tracked.txt worktree status = %q, want modified (M)", got[2])
	}
	if got := rowFor(t, tbl, "Path", "staged.txt"); got[1] != "A" {
		t.Errorf("staged.txt staged status = %q, want added (A)", got[1])
	}
	if tbl.Total != len(tbl.Rows) {
		t.Errorf("Total = %d, want %d", tbl.Total, len(tbl.Rows))
	}
}

func TestStatusOnANonRepositoryFailsWithAClearError(t *testing.T) {
	dir := t.TempDir()
	_, err := runStatus(context.Background(), req(t, dir, nil))
	if err == nil {
		t.Fatal("expected an error opening a non-repository")
	}
}
