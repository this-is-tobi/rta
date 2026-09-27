package git

import (
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Both sides of a changed file are read whole to line-diff them, and nothing
// bounded either: a generated asset that changed put its whole size into the
// process twice. Over the bound the file is named in the diff rather than
// diffed, and the rest of the change is still shown.
func TestAChangedFileOverTheBoundIsNamedRatherThanRead(t *testing.T) {
	saved := maxDiffBytes
	maxDiffBytes = 64
	t.Cleanup(func() { maxDiffBytes = saved })

	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "small.txt", "v1\n", "initial")
	commitFile(t, repo, dir, "big.bin", "x\n", "asset")
	writeFile(t, dir, "small.txt", "v1\nv2\n")
	writeFile(t, dir, "big.bin", strings.Repeat("payload ", 40))

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+v2") {
		t.Errorf("the small change is missing from the diff:\n%s", body)
	}
	if strings.Contains(body, "payload") {
		t.Errorf("the large file's content was read and diffed:\n%s", body)
	}
	if !strings.Contains(body, "big.bin changed, larger than") {
		t.Errorf("the large file is not named in the diff:\n%s", body)
	}
}

// A --commit diff reads both sides of every change, and a root commit is now
// diffed in full: a file over the per-file bound is named, as the worktree
// diff names one, and past the commit's budget the rest is counted rather
// than read — the change that fits is still shown.
func TestACommitDiffIsBoundedPerFileAndInAll(t *testing.T) {
	savedFile, savedAll := maxDiffBytes, maxTotalDiffBytes
	maxDiffBytes, maxTotalDiffBytes = 64, 100
	t.Cleanup(func() { maxDiffBytes, maxTotalDiffBytes = savedFile, savedAll })

	dir, repo := testRepo(t)
	writeFile(t, dir, "a.txt", "small change\n")
	writeFile(t, dir, "big.bin", strings.Repeat("payload ", 40))
	writeFile(t, dir, "b.txt", strings.Repeat("b", 60)+"\n")
	writeFile(t, dir, "c.txt", strings.Repeat("c", 60)+"\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("root", &git.CommitOptions{Author: signature()}); err != nil {
		t.Fatal(err)
	}

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(body, "+small change") {
		t.Errorf("the change that fits is missing:\n%s", body)
	}
	if strings.Contains(body, "payload") || !strings.Contains(body, "big.bin changed, larger than") {
		t.Errorf("the file over the per-file bound was read, or not named:\n%s", body)
	}
	if !strings.Contains(body, "1 more file changed and not diffed") {
		t.Errorf("what is past the commit's budget is not counted:\n%s", body)
	}
}

// The working tree's diff has the budget a commit's has. It held each file to
// the per-file bound and nothing held the lot: twenty untracked logs just
// under that bound were all read and line-diffed in one ungated call, and
// three hundred megabytes on disk cost the server three gigabytes. Past the
// budget the rest is counted, in the order the diff lists its files, and the
// changes that fit are still shown.
func TestAWorktreeDiffIsBoundedInAll(t *testing.T) {
	savedFile, savedAll := maxDiffBytes, maxTotalDiffBytes
	maxDiffBytes, maxTotalDiffBytes = 64, 100
	t.Cleanup(func() { maxDiffBytes, maxTotalDiffBytes = savedFile, savedAll })

	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "a.txt", "v1\nsmall change\n")
	writeFile(t, dir, "b.log", strings.Repeat("b", 60)+"\n")
	writeFile(t, dir, "c.log", strings.Repeat("c", 60)+"\n")
	writeFile(t, dir, "d.log", strings.Repeat("d", 60)+"\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+small change") || !strings.Contains(body, "+"+strings.Repeat("b", 60)) {
		t.Errorf("the changes that fit are missing:\n%s", body)
	}
	if strings.Contains(body, strings.Repeat("c", 60)) || strings.Contains(body, strings.Repeat("d", 60)) {
		t.Errorf("a file past the budget was read and diffed:\n%s", body)
	}
	if !strings.Contains(body, "2 more files changed and not diffed") {
		t.Errorf("what is past the budget is not counted:\n%s", body)
	}
}

// Matching lines costs the product of the lines and the lines that changed,
// and go-git's line diff ran with a one-hour timeout: a fully rewritten 2 MiB
// file cost one diff 30 s of CPU. Past matchTime what is left is shown
// removed and added, a correct patch and a coarser one, and the file is named
// as diffed coarsely, on either kind of diff.
func TestADiffPastItsMatchingTimeIsCoarseAndSaysSo(t *testing.T) {
	saved := matchTime
	matchTime = -time.Second
	t.Cleanup(func() { matchTime = saved })

	// Two orders of two lines: nothing to match at either end but the last
	// line, and far more matching than the deadline is first looked at after.
	r := rand.New(rand.NewSource(1))
	first, _ := randomText(r, 3000, 2)
	second, _ := randomText(r, 3000, 2)
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "x\n"+first+"end\n", "first")
	commitFile(t, repo, dir, "a.txt", "y\n"+second+"end\n", "second")
	writeFile(t, dir, "a.txt", "x\n"+first+"end\n")

	for name, c := range map[string]struct {
		values map[string]any
		lines  []string
	}{
		"worktree": {nil, []string{"-y\n", "+x\n", " end\n"}},
		"commit":   {map[string]any{"commit": "master"}, []string{"-x\n", "+y\n", " end\n"}},
	} {
		body := text(t, runDiff, req(t, dir, c.values))
		if !strings.Contains(body, "a.txt changed, diffed coarsely") {
			t.Errorf("%s: the file matched coarsely is not named:\n%s", name, body)
		}
		// Coarse, and still the change: the line both sides end with is kept,
		// and the lines either side of what changed are shown.
		for _, want := range c.lines {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the coarse patch has no %q line", name, want)
			}
		}
	}
}

// A caller that has stopped waiting gets no diff rather than the files it had
// got to, which would read as the whole of a smaller change.
func TestADiffTheCallerCancelledAnswersNothing(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "first")
	commitFile(t, repo, dir, "a.txt", "v2\n", "second")
	writeFile(t, dir, "a.txt", "v3\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, values := range map[string]map[string]any{
		"worktree": nil,
		"commit":   {"commit": "master"},
	} {
		v, err := runDiff(ctx, req(t, dir, values))
		if code := errCode(err); code != "git.diff.cancelled" {
			t.Errorf("%s: a cancelled diff answered %v, %q; want git.diff.cancelled", name, v, code)
		}
	}
}

// A commit's patch is built here rather than by go-git, which had no bound on
// the time it spent matching lines, and it is go-git's patch file for file:
// an edit, an addition, a deletion, a rename, a mode change, a binary file
// and an empty one.
func TestACommitPatchIsGoGitsPatch(t *testing.T) {
	dir, repo := testRepo(t)
	writeFile(t, dir, "edit.txt", "one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\n")
	writeFile(t, dir, "gone.txt", "going\n")
	writeFile(t, dir, "moved.txt", strings.Repeat("a line that moves with its file\n", 20))
	writeFile(t, dir, "run.sh", "echo hi\n")
	writeFile(t, dir, "blob.bin", "x\x00y")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commitAll := func(msg string) {
		t.Helper()
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: signature()}); err != nil {
			t.Fatal(err)
		}
	}
	commitAll("before")

	writeFile(t, dir, "edit.txt", "one\ntwo\nTHREE\nfour\nfive\nsix\nseven\neight\nnine\n")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "moved.txt"), filepath.Join(dir, "moved-here.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "blob.bin", "x\x00z")
	writeFile(t, dir, "empty.txt", "")
	writeFile(t, dir, "added.txt", "new\nlines\n")
	commitAll("after")

	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	parent, err := commit.Parent(0)
	if err != nil {
		t.Fatal(err)
	}
	want, err := parent.Patch(commit)
	if err != nil {
		t.Fatal(err)
	}
	from, _ := parent.Tree()
	to, _ := commit.Tree()
	changes, err := object.DiffTreeWithOptions(t.Context(), from, to, object.DefaultDiffTreeOptions)
	if err != nil {
		t.Fatal(err)
	}
	got, coarse, err := commitPatch(t.Context(), repo, changes, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got != want.String() || len(coarse) != 0 {
		t.Errorf("patch:\n%s\nwant go-git's:\n%s", got, want.String())
	}
	for _, part := range []string{"rename from moved.txt", "new mode 100755", "Binary files", "+nine", "deleted file mode"} {
		if !strings.Contains(got, part) {
			t.Errorf("the fixture no longer covers %q:\n%s", part, got)
		}
	}
}

// go-git finds a renamed file by comparing every deleted file with every
// added one, reading the added one again for each, before anything is held
// to the diff's budget: a hundred files moved and rewritten cost one diff
// 7.7 s and half a gigabyte. Past the budget a rename is matched by identical
// content only, which reads nothing, and the diff says so.
func TestACommitDiffMatchesRenamesBySimilarContentWithinItsBudget(t *testing.T) {
	saved := maxTotalDiffBytes
	t.Cleanup(func() { maxTotalDiffBytes = saved })

	dir, repo := testRepo(t)
	lines := strings.Repeat("a line of the file\n", 20)
	writeFile(t, dir, "old.txt", lines)
	writeFile(t, dir, "same.txt", "x\n")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commitAll := func(msg string) {
		t.Helper()
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: signature()}); err != nil {
			t.Fatal(err)
		}
	}
	commitAll("before")
	for _, name := range []string{"old.txt", "same.txt"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "new.txt", lines+"one more\n")
	writeFile(t, dir, "moved.txt", "x\n")
	commitAll("moved")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(body, "rename from old.txt") || strings.Contains(body, "identical content only") {
		t.Errorf("within the budget, the edited file is not diffed as renamed:\n%s", body)
	}

	maxTotalDiffBytes = 100
	body = text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if strings.Contains(body, "rename from old.txt") || !strings.Contains(body, "renames matched by identical content only") {
		t.Errorf("past the budget, renames are still looked for by content, or it is not said:\n%s", body)
	}
	if !strings.Contains(body, "rename from same.txt") {
		t.Errorf("past the budget, a file moved unchanged is no longer diffed as renamed:\n%s", body)
	}
}

// Bytes did not hold rename detection: twenty thousand files of a few bytes
// moved and rewritten are four hundred million pairs of reads that add up to
// little, and one diff of them was still running after two minutes. It stops
// at git's own limit on the files it compares, and at half the call's time,
// and the diff says which.
func TestACommitDiffMatchesRenamesBySimilarContentWithinGitsLimitAndItsTime(t *testing.T) {
	savedLimit, savedTime := renameLimit, matchTime
	t.Cleanup(func() { renameLimit, matchTime = savedLimit, savedTime })

	dir, repo := testRepo(t)
	lines := strings.Repeat("a line of the file\n", 20)
	for _, name := range []string{"a1.txt", "a2.txt"} {
		writeFile(t, dir, name, name+lines)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commitAll := func(msg string) {
		t.Helper()
		if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := wt.Commit(msg, &git.CommitOptions{Author: signature()}); err != nil {
			t.Fatal(err)
		}
	}
	commitAll("before")
	for _, name := range []string{"a1.txt", "a2.txt"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "b"+name[1:], name+lines+"one more\n")
	}
	commitAll("moved")

	diff := func() string {
		t.Helper()
		return text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	}
	if body := diff(); !strings.Contains(body, "rename from a1.txt") || strings.Contains(body, "identical content only") {
		t.Errorf("within the limit, the edited files are not diffed as renamed:\n%s", body)
	}
	renameLimit = 1
	if body := diff(); strings.Contains(body, "rename from") ||
		!strings.Contains(body, "by similar content is not tried past 1 file added or deleted") {
		t.Errorf("past the limit, renames are still looked for by content, or it is not said:\n%s", body)
	}
	renameLimit, matchTime = savedLimit, time.Nanosecond
	if body := diff(); strings.Contains(body, "rename from") || !strings.Contains(body, "by similar content takes more than") {
		t.Errorf("past its time, renames are still looked for by content, or it is not said:\n%s", body)
	}
}

// Bytes held what a diff read and not how many files it read them from: a
// commit adding two hundred thousand files of a few bytes cost one git_diff
// 22 s of CPU. A diff looks at so many files, in the order it lists them,
// and counts the rest in its own shape, a --commit diff and the working
// tree's alike.
func TestADiffLooksAtABoundedNumberOfFiles(t *testing.T) {
	saved := maxDiffFiles
	maxDiffFiles = 2
	t.Cleanup(func() { maxDiffFiles = saved })

	dir, repo := testRepo(t)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		writeFile(t, dir, name, name+" v1\n")
	}
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("four files", &git.CommitOptions{Author: signature()}); err != nil {
		t.Fatal(err)
	}
	const counted = "2 more files changed and not looked at: one diff looks at no more than 2 files"

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "HEAD"}))
	if !strings.Contains(body, "+a.txt v1") || !strings.Contains(body, "+b.txt v1") ||
		strings.Contains(body, "c.txt v1") || !strings.Contains(body, counted) {
		t.Errorf("the commit's diff is not the first two files and a count of the rest:\n%s", body)
	}

	for _, name := range []string{"a.txt", "b.txt", "c.txt", "d.txt"} {
		writeFile(t, dir, name, name+" v2\n")
	}
	body = text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+a.txt v2") || !strings.Contains(body, "+b.txt v2") ||
		strings.Contains(body, "c.txt v2") || !strings.Contains(body, counted) {
		t.Errorf("the working tree's diff is not the first two files and a count of the rest:\n%s", body)
	}
}
