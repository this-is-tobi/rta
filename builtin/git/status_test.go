package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/view"
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

// lowerStatusTime holds one status to d.
func lowerStatusTime(t *testing.T, d time.Duration) {
	t.Helper()
	saved := statusTime
	statusTime = d
	t.Cleanup(func() { statusTime = saved })
}

// go-git's status costs what the working tree holds, and nothing bounded it:
// a hundred thousand rewritten files cost one git.status 7 s. Each call that
// reads the working tree's status is refused once its time has run out,
// naming what it had read, rather than answered with the part it had: that
// would read as a cleaner tree than the one there. A commit's diff reads no
// working tree, and is not held to it.
func TestAStatusPastItsTimeIsRefused(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.txt", "v2\n")
	writeFile(t, dir, "new.txt", "new\n")
	lowerStatusTime(t, -time.Second)
	for name, run := range map[string]func() error{
		"git.status": func() error { _, err := runStatus(context.Background(), req(t, dir, nil)); return err },
		"git.diff":   func() error { _, err := runDiff(context.Background(), req(t, dir, nil)); return err },
		"git.overview": func() error {
			_, err := runOverview(context.Background(), req(t, dir, nil))
			return err
		},
	} {
		err := run()
		var verr *view.Error
		if !errors.As(err, &verr) || verr.Code != "git.status.timeout" {
			t.Errorf("%s past its time = %v, want git.status.timeout", name, err)
			continue
		}
		if !strings.Contains(verr.Message, dir) || !strings.Contains(verr.Message, "had listed 0 directories") ||
			!strings.Contains(verr.Hint, "git status") {
			t.Errorf("%s refusal = %+v, want the working tree, what it had read, and git status named", name, verr)
		}
	}
	if body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"})); !strings.Contains(body, "+v1") {
		t.Errorf("a commit's diff was held to the status's time:\n%s", body)
	}
}

// A caller that has stopped waiting gets no status either.
func TestAStatusTheCallerCancelledAnswersNothing(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.txt", "v2\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runStatus(ctx, req(t, dir, nil)); errCode(err) != "git.status.cancelled" {
		t.Errorf("a cancelled status = %v, want git.status.cancelled", err)
	}
}

// Once the time has run out, nothing of the working tree is read: each
// listing, open, stat and link go-git asks for fails, which ends its walk.
func TestTheStatusFilesReadNothingPastTheTime(t *testing.T) {
	dir, _ := testRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	budget := &statusBudget{ctx: context.Background(), deadline: time.Now().Add(-time.Second)}
	fs := statusFiles{Filesystem: osfs.New(dir), budget: budget}
	_, openErr := fs.Open("a.txt")
	_, dirErr := fs.ReadDir("")
	_, lstatErr := fs.Lstat("a.txt")
	_, statErr := fs.Stat("a.txt")
	_, linkErr := fs.Readlink("a.txt")
	for op, err := range map[string]error{
		"open": openErr, "readdir": dirErr, "lstat": lstatErr, "stat": statErr, "readlink": linkErr,
	} {
		if !errors.Is(err, errStatusTime) {
			t.Errorf("%s past the time = %v, want it refused", op, err)
		}
	}
}

// A file opened before the time ran out is read no further once it has: go-git
// hashes a file whose timestamp moved whole, and one sparse file of 8 GiB
// held a status for 8.5 s with no listing or open between.
func TestAFileTheStatusOpenedIsReadNoFurtherPastTheTime(t *testing.T) {
	dir, _ := testRepo(t)
	writeFile(t, dir, "a.txt", strings.Repeat("a", 64<<10))
	budget := &statusBudget{ctx: context.Background(), deadline: time.Now().Add(time.Hour)}
	f, err := statusFiles{Filesystem: osfs.New(dir), budget: budget}.Open("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 32<<10)
	if n, err := f.Read(buf); err != nil || n == 0 {
		t.Fatalf("read within the time = %d, %v", n, err)
	}
	budget.deadline = time.Now().Add(-time.Second)
	if _, err := f.Read(buf); !errors.Is(err, errStatusTime) {
		t.Errorf("read past the time = %v, want it refused", err)
	}
	if verr := budget.refusal(dir); verr == nil || verr.Code != "git.status.timeout" {
		t.Errorf("refusal = %+v, want git.status.timeout", verr)
	}
}

// The working tree's own .gitignore, which go-git's comparison was handed `*`
// for, is hashed again from the disk to put that right, and that read is held
// to the time too: a tracked .gitignore of 4 GB, touched, held a status 2 s
// past it. Past the time it is left modified, and the status refused.
func TestTheRootIgnoreFileIsHashedNoFurtherPastTheTime(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, ".gitignore", "*.log\n", "initial")
	idx, err := repo.Storer.Index()
	if err != nil {
		t.Fatal(err)
	}
	modified := func() git.Status {
		status := git.Status{}
		status.File(".gitignore").Staging, status.File(".gitignore").Worktree = git.Unmodified, git.Modified
		return status
	}
	status := modified()
	budget := &statusBudget{ctx: context.Background(), deadline: time.Now().Add(time.Hour)}
	if restoreRootIgnore(osfs.New(dir), idx, status, budget); len(status) != 0 {
		t.Errorf("within the time, status = %v, want the .gitignore git records put right", status)
	}
	status = modified()
	budget.deadline = time.Now().Add(-time.Second)
	if restoreRootIgnore(osfs.New(dir), idx, status, budget); status.File(".gitignore").Worktree != git.Modified {
		t.Errorf("past the time, status = %v, want the .gitignore left as the comparison saw it", status)
	}
	if verr := budget.refusal(dir); verr == nil || verr.Code != "git.status.timeout" {
		t.Errorf("refusal = %+v, want git.status.timeout", verr)
	}
}

// The matching of untracked files against ignore patterns costs the files
// times the patterns, and ten thousand of each cost 30 s within the bounds:
// it stops where the time runs out, and the refusal says how far it got.
func TestTheIgnoreMatchingStopsAtTheTime(t *testing.T) {
	dir, _ := testRepo(t)
	writeFile(t, dir, ".gitignore", strings.Repeat("*a*a*a*a*a*a*a*b\n", 1000))
	status := git.Status{}
	for _, name := range []string{"x1", "x2", "x3"} {
		status.File(name).Worktree = git.Untracked
	}
	budget := &statusBudget{ctx: context.Background(), deadline: time.Now().Add(time.Hour)}
	read := newIgnoresRead(nil, false, osfs.New(dir), budget)
	read.dir("")
	budget.deadline = time.Now().Add(-time.Second)
	read.dropIgnored(status)
	if len(status) != 3 {
		t.Errorf("status = %v, want every path left as it was", status)
	}
	verr := budget.refusal(dir)
	if verr == nil || verr.Code != "git.status.timeout" ||
		!strings.Contains(verr.Message, "matched 0 of its 3 untracked files against the ignore patterns") {
		t.Errorf("refusal = %+v, want how far the matching got", verr)
	}
	if lastMatch([]ignoreFile{read.files[".gitignore"]}, "aaaaaaaab", "aaaaaaaab", false, false, budget) != nil {
		t.Error("a pattern matched once the time had run out")
	}
}
