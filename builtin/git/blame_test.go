package git

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
		_, err = runBlame(context.Background(), req(t, dir, map[string]any{"file": "a.txt"}))
		if code := errCode(err); code != "git.blame.toolarge" {
			t.Fatalf("blame of a file added beside more than the budget: %q, want git.blame.toolarge", code)
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

	dir, repo := testRepo(t)
	for i := range 10 {
		commitFile(t, repo, dir, "a.txt", strings.Repeat(fmt.Sprintf("%d\n", i), 25), fmt.Sprintf("version %d", i))
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
