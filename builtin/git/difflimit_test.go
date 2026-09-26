package git

import (
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
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
	savedFile, savedAll := maxDiffBytes, maxCommitDiffBytes
	maxDiffBytes, maxCommitDiffBytes = 64, 100
	t.Cleanup(func() { maxDiffBytes, maxCommitDiffBytes = savedFile, savedAll })

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
