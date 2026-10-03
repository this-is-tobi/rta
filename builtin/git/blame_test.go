package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/this-is-tobi/rta/pkg/view"
)

func TestBlameAttributesEachLineToTheCommitThatIntroducedIt(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\nline two\n", "first commit")
	commitFile(t, repo, dir, "a.txt", "line one\nline two\nline three\n", "second commit")
	t.Chdir(dir)

	tbl := table(t, runBlame, req(t, dir, map[string]any{"file": "a.txt"}))
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(tbl.Rows))
	}
	if got := tbl.Rows[0][4]; got != "line one" {
		t.Errorf("line 1 content = %q, want %q", got, "line one")
	}
	if got := tbl.Rows[2][4]; got != "line three" {
		t.Errorf("line 3 content = %q, want %q", got, "line three")
	}
	if got := tbl.Rows[0][2]; got != "Ada Lovelace" {
		t.Errorf("line 1 author = %q, want %q", got, "Ada Lovelace")
	}
}

func TestBlameOnAnUntrackedFileFailsWithAClearError(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	t.Chdir(dir)

	_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "nope.txt"}))
	if err == nil {
		t.Fatal("expected an error blaming a file that was never committed")
	}
}

// A directory is there, and is not a file: it was answered "file not found",
// which blames the wrong thing, beside a hint about a file never committed.
func TestBlameOnADirectorySaysItIsOne(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "deploy/values.yaml", "replicas: 2\n", "initial")
	t.Chdir(dir)

	_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "deploy"}))
	verr := view.AsError(err, "x")
	if err == nil || verr.Code != "git.blame.isdir" || !strings.Contains(verr.Message, "deploy is a directory") {
		t.Fatalf("blaming a directory: %v, want git.blame.isdir naming it as one", err)
	}
	_, err = runBlame(context.Background(), req(t, dir, map[string]any{"file": "deploy/nope.yaml"}))
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "git.blame.failed" {
		t.Errorf("blaming a file never committed: %v, want git.blame.failed", err)
	}
}

// A file over the bound a diff holds one file to is refused by its size, not
// read: blame reads it whole at every commit that touched it and answers a
// row per line, and a 100 MB file cost one ungated call 3.2 GB.
func TestBlameRefusesAFileOverTheBound(t *testing.T) {
	saved := maxDiffBytes
	maxDiffBytes = 64
	t.Cleanup(func() { maxDiffBytes = saved })
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "big.txt", strings.Repeat("line\n", 40), "big")
	t.Chdir(dir)

	_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "big.txt"}))
	if code := errCode(err); code != "git.blame.toolarge" {
		t.Fatalf("blame of a file over the bound: %q, want git.blame.toolarge", code)
	}
}

// The bound holds for the whole history a blame reads, not only for HEAD: a
// version over it in an earlier commit was read whole, and so was every file
// changed in the commit that added the blamed one, which go-git's blame diffs
// whole to look for a rename. A history within the budget is still blamed.
func TestBlameHoldsTheHistoryItReadsToTheBounds(t *testing.T) {
	savedFile, savedBlame := maxDiffBytes, maxBlameBytes
	maxDiffBytes, maxBlameBytes = 64, 100
	t.Cleanup(func() { maxDiffBytes, maxBlameBytes = savedFile, savedBlame })

	t.Run("a version over the bound", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "a.txt", strings.Repeat("line\n", 40), "big")
		commitFile(t, repo, dir, "a.txt", "line\n", "small again")
		t.Chdir(dir)
		_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "a.txt"}))
		if code := errCode(err); code != "git.blame.toolarge" {
			t.Fatalf("blame over a version past the bound: %q, want git.blame.toolarge", code)
		}
	})

	// go-git's blame built the whole patch of the commit a file was added
	// in to look for a rename, reading every file beside it; the rename
	// search now compares trees, and reads a file only where one was
	// deleted that the added one could have been renamed from.
	t.Run("added beside more than the budget", func(t *testing.T) {
		dir, repo := testRepo(t)
		commitFile(t, repo, dir, "seed.txt", "seed\n", "seed")
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"b.log", "c.log"} {
			writeFile(t, dir, name, strings.Repeat(name[:1], 60)+"\n")
			if _, err := wt.Add(name); err != nil {
				t.Fatal(err)
			}
		}
		commitFile(t, repo, dir, "a.txt", "one line\n", "a.txt, beside two logs")
		t.Chdir(dir)
		if tbl := table(t, runBlame, req(t, dir, map[string]any{"file": "a.txt"})); len(tbl.Rows) != 1 {
			t.Errorf("rows = %d, want the one line blamed without reading the files beside it", len(tbl.Rows))
		}
	})

	t.Run("renamed beside more than the budget", func(t *testing.T) {
		dir, repo := testRepo(t)
		writeFile(t, dir, "old.txt", "one line\nsecond\n")
		writeFile(t, dir, "b.log", strings.Repeat("b", 60)+"\n")
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatal(err)
		}
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit("seed", &git.CommitOptions{Author: signature()}); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"old.txt", "b.log"} {
			if _, err := wt.Remove(name); err != nil {
				t.Fatal(err)
			}
		}
		writeFile(t, dir, "c.log", strings.Repeat("c", 60)+"\n")
		if _, err := wt.Add("c.log"); err != nil {
			t.Fatal(err)
		}
		commitFile(t, repo, dir, "a.txt", "one line\nsecond\nthird\n", "renamed, beside a log replaced")
		t.Chdir(dir)
		_, err = runBlame(context.Background(), req(t, dir, map[string]any{"file": "a.txt"}))
		if code := errCode(err); code != "git.blame.toolarge" {
			t.Fatalf("blame of a file renamed beside more than the budget: %q, want git.blame.toolarge", code)
		}
	})

	t.Run("a long history within the budget", func(t *testing.T) {
		savedBlame := maxBlameBytes
		maxBlameBytes = 1000
		t.Cleanup(func() { maxBlameBytes = savedBlame })
		dir, repo := testRepo(t)
		content := ""
		for i := range 10 {
			content += fmt.Sprintf("%d\n", i)
			commitFile(t, repo, dir, "a.txt", content, fmt.Sprintf("line %d", i))
		}
		t.Chdir(dir)
		if tbl := table(t, runBlame, req(t, dir, map[string]any{"file": "a.txt"})); len(tbl.Rows) != 10 {
			t.Errorf("rows = %d, want a row for each of the 10 lines", len(tbl.Rows))
		}
	})
}

// Each step of a blame's walk held to a diff's bounds still left the walk as
// a whole unbounded: a history of versions each just under the per-file
// bound, every one read whole and line-diffed against the next, cost one
// call half a gigabyte and minutes of CPU. The blame reads maxBlameBytes in
// all, and a history past it is refused with the budget and the file named.
func TestBlameHoldsItsWholeWalkToABudget(t *testing.T) {
	savedFile, savedBlame := maxDiffBytes, maxBlameBytes
	maxDiffBytes, maxBlameBytes = 64, 200
	t.Cleanup(func() { maxDiffBytes, maxBlameBytes = savedFile, savedBlame })

	// Each version shares its first lines with the one before, so that the
	// walk goes back through all of them: a version rewritten whole ends it.
	dir, repo := testRepo(t)
	for i := range 10 {
		commitFile(t, repo, dir, "a.txt", strings.Repeat("x\n", 20)+fmt.Sprintf("%d\n", i), fmt.Sprintf("version %d", i))
	}
	t.Chdir(dir)
	_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "a.txt"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "git.blame.toolarge" {
		t.Fatalf("blame of a history past the budget: %v, want git.blame.toolarge", err)
	}
	if !strings.Contains(verr.Message, "a.txt") || !strings.Contains(verr.Message, "200 B") {
		t.Errorf("message = %q, want it to name the file and the budget", verr.Message)
	}
}

// The blame is rta's own, by git's rule of passing the blame to the parents,
// and it answers what go-git's did on a history with a rename and a merge:
// each line to the commit that last changed it, on whichever side of the
// merge that was.
func TestBlameFollowsARenameAndBothSidesOfAMerge(t *testing.T) {
	dir, repo := testRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}
	version := func(changed map[int]string) string {
		var b strings.Builder
		for i, l := range lines {
			if c, ok := changed[i]; ok {
				l = c
			}
			b.WriteString(l + "\n")
		}
		return b.String()
	}
	first := commitFile(t, repo, dir, "a.txt", version(nil), "first")
	third := commitFile(t, repo, dir, "a.txt", version(map[int]string{2: "THREE"}), "third line")
	if _, err := wt.Remove("a.txt"); err != nil {
		t.Fatal(err)
	}
	renamed := commitFile(t, repo, dir, "b.txt", version(map[int]string{2: "THREE", 4: "FIVE"}), "renamed")

	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/side", Create: true}); err != nil {
		t.Fatal(err)
	}
	side := commitFile(t, repo, dir, "b.txt", version(map[int]string{2: "THREE", 4: "FIVE", 7: "EIGHT"}), "on the side")
	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/master"}); err != nil {
		t.Fatal(err)
	}
	main := commitFile(t, repo, dir, "b.txt", version(map[int]string{0: "ONE", 2: "THREE", 4: "FIVE"}), "on master")
	writeFile(t, dir, "b.txt", version(map[int]string{0: "ONE", 2: "THREE", 4: "FIVE", 7: "EIGHT"}))
	if _, err := wt.Add("b.txt"); err != nil {
		t.Fatal(err)
	}
	merge, err := wt.Commit("merge", &git.CommitOptions{Author: signature(), Parents: []plumbing.Hash{main, side}})
	if err != nil {
		t.Fatal(err)
	}

	head, err := repo.CommitObject(merge)
	if err != nil {
		t.Fatal(err)
	}
	got, err := blame(t.Context(), &boundedHistory{EncodedObjectStorer: repo.Storer}, head, "b.txt", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]plumbing.Hash{0: main, 2: third, 4: renamed, 7: side}
	for i, l := range got.lines {
		w, ok := want[i]
		if !ok {
			w = first
		}
		if l.commit.Hash != w || l.boundary {
			t.Errorf("line %d (%s) is blamed on %s (boundary %v), want %s", i+1, got.text(i), shortHash(l.commit.Hash),
				l.boundary, shortHash(w))
		}
	}
	theirs, err := git.Blame(head, "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range theirs.Lines {
		if got.lines[i].commit.Hash != l.Hash || got.text(i) != l.Text {
			t.Errorf("line %d: %s %q, go-git's blame %s %q", i+1, shortHash(got.lines[i].commit.Hash), got.text(i),
				shortHash(l.Hash), l.Text)
		}
	}
}

// go-git's blame had no bound on its time: three versions of a 10 MiB file
// kept one call busy for more than ten minutes, inside every byte budget.
// The walk stops at the call's deadline, and a line it has not traced by
// then carries the commit it had reached, marked ^ as git marks a boundary,
// with a warning saying how many and why.
func TestBlamePastItsDeadlineMarksWhatItDidNotTrace(t *testing.T) {
	saved := matchTime
	matchTime = -time.Second
	t.Cleanup(func() { matchTime = saved })

	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "one\ntwo\n", "first")
	head := commitFile(t, repo, dir, "a.txt", "one\ntwo\nthree\n", "second")
	t.Chdir(dir)

	tbl := table(t, runBlame, req(t, dir, map[string]any{"file": "a.txt"}))
	for _, row := range tbl.Rows {
		if row[1] != "^"+shortHash(head) {
			t.Errorf("line %s is blamed on %s, want the boundary it stopped at, ^%s", row[0], row[1], shortHash(head))
		}
	}
	if len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "git.blame.partial" ||
		!strings.Contains(tbl.Warnings[0].Message, "3 lines") {
		t.Errorf("warnings = %+v, want git.blame.partial naming the 3 lines", tbl.Warnings)
	}
}

// A caller that has stopped waiting gets no blame rather than a walk it
// stopped partway, which would read as lines written later than they were.
func TestBlameTheCallerCancelledAnswersNothing(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "one\n", "first")
	t.Chdir(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := runBlame(ctx, req(t, dir, map[string]any{"file": "a.txt"}))
	if code := errCode(err); code != "git.blame.cancelled" {
		t.Errorf("a cancelled blame answered %q, want git.blame.cancelled", code)
	}
}

// Where the file was added or renamed, the blame compares the commit's tree
// with its parent's, and that comparison reads every file it deleted and
// added, pair by pair: a file added beside twenty thousand files of a few
// bytes moved and rewritten kept one blame busy for more than two minutes,
// inside its byte budget. Each read looks at the deadline, and the walk stops
// at the one that passes it, as it stops between commits.
func TestBlameStopsItsRenameSearchAtTheDeadline(t *testing.T) {
	dir, repo := testRepo(t)
	lines := strings.Repeat("a line of the file\n", 20)
	commitFile(t, repo, dir, "old.txt", lines, "first")
	if err := os.Remove(filepath.Join(dir, "old.txt")); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Remove("old.txt"); err != nil {
		t.Fatal(err)
	}
	head := commitFile(t, repo, dir, "new.txt", lines+"one more\n", "moved and edited")

	store := &boundedHistory{EncodedObjectStorer: repo.Storer, deadline: time.Now().Add(-time.Second)}
	search := func() (string, error) {
		t.Helper()
		commit, err := object.GetCommit(store, head)
		if err != nil {
			t.Fatal(err)
		}
		parent, err := commit.Parent(0)
		if err != nil {
			t.Fatal(err)
		}
		from, err := parent.Tree()
		if err != nil {
			t.Fatal(err)
		}
		to, err := commit.Tree()
		if err != nil {
			t.Fatal(err)
		}
		name, _, err := renamedFrom(t.Context(), store.deadline, from, to, "new.txt")
		return name, err
	}
	if _, err := search(); !errors.Is(err, errPastDeadline) {
		t.Errorf("the rename search past the deadline: %v, want errPastDeadline", err)
	}
	// And the comparison of the trees stops at it, before any file is read.
	if _, _, err := renamedFrom(t.Context(), time.Now().Add(-time.Second), nil, nil, "new.txt"); !errors.Is(err, errPastDeadline) {
		t.Errorf("the comparison of the trees past the deadline: %v, want errPastDeadline", err)
	}
	store.deadline = time.Now().Add(time.Hour)
	if name, err := search(); err != nil || name != "old.txt" {
		t.Errorf("the rename search within its time found %q, %v, want old.txt", name, err)
	}
}
