package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A path whose kind changed is T, as git reports it, where go-git has only
// M: a file that became a symbolic link, a link that became a file, and a
// link a named pipe took the place of, which git counts as a file. A file a
// pipe took the place of is M in git too. Judged by a lstat, and the pipe is
// never opened: open(2) on one with no writer waits for one.
func TestStatusReportsAChangeOfKindAsGitDoes(t *testing.T) {
	dir, repo := testRepo(t)
	for _, name := range []string{"file-to-link", "file-to-pipe", "staged"} {
		writeFile(t, dir, name, name+"\n")
	}
	for _, name := range []string{"link-to-file", "link-to-pipe"} {
		if err := os.Symlink("staged", filepath.Join(dir, name)); err != nil {
			t.Skipf("no symbolic links here: %v", err)
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{"file-to-link", "file-to-pipe", "staged", "link-to-file", "link-to-pipe"}
	for _, name := range kinds {
		if _, err := wt.Add(name); err != nil {
			t.Fatal(err)
		}
	}
	commitFile(t, repo, dir, "keep", "keep\n", "initial")
	for _, name := range kinds {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("keep", filepath.Join(dir, "file-to-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("keep", filepath.Join(dir, "staged")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "link-to-file", "now a file\n")
	for _, name := range []string{"file-to-pipe", "link-to-pipe"} {
		if err := mkfifo(filepath.Join(dir, name)); err != nil {
			t.Skipf("no named pipes here: %v", err)
		}
	}
	if _, err := wt.Add("staged"); err != nil {
		t.Fatal(err)
	}

	done := make(chan []string, 1)
	go func() {
		var got []string
		for _, r := range table(t, runStatus, req(t, dir, nil)).Rows {
			got = append(got, r[1]+r[2]+" "+r[0])
		}
		done <- got
	}()
	select {
	case got := <-done:
		want := []string{" T file-to-link", " M file-to-pipe", " T link-to-file", " T link-to-pipe", "T  staged"}
		if strings.Join(got, "; ") != strings.Join(want, "; ") {
			t.Errorf("status = %q, want %q, as git status --porcelain reports it", got, want)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the status is waiting on a named pipe")
	}
}
