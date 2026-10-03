package git

import (
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// mergeOf makes a merge commit of master and side, taking the working tree as
// it stands, and returns it with its first parent.
func mergeOf(t *testing.T, dir string, repo *git.Repository) (merge, first plumbing.Hash) {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, dir, "base.txt", "base\n", "base")
	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/side", Create: true}); err != nil {
		t.Fatal(err)
	}
	side := commitFile(t, repo, dir, "side.txt", "from the side\n", "on the side")
	if err := wt.Checkout(&git.CheckoutOptions{Branch: "refs/heads/master"}); err != nil {
		t.Fatal(err)
	}
	first = commitFile(t, repo, dir, "main.txt", "from master\n", "on master")
	writeFile(t, dir, "side.txt", "from the side\n")
	if _, err := wt.Add("side.txt"); err != nil {
		t.Fatal(err)
	}
	merge, err = wt.Commit("merge", &git.CommitOptions{Author: signature(), Parents: []plumbing.Hash{first, side}})
	if err != nil {
		t.Fatal(err)
	}
	return merge, first
}

// `--commit <merge>` diffs against the first parent, as `git diff <merge>^1
// <merge>` does, and `git show` prints a combined diff instead. Nothing said
// which of them this was, so the patch of what the merge brought in read as
// everything the merge changed.
func TestDiffOfAMergeSaysItIsAgainstTheFirstParent(t *testing.T) {
	dir, repo := testRepo(t)
	merge, first := mergeOf(t, dir, repo)

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": merge.String()}))
	if !strings.Contains(body, "+from the side") {
		t.Errorf("the merge's diff does not show what it brought in:\n%s", body)
	}
	want := shortHash(merge) + " is a merge: this is its diff against its first parent " + shortHash(first)
	if !strings.Contains(body, want) {
		t.Errorf("the merge's diff does not say it is against the first parent (want %q):\n%s", want, body)
	}

	plain := text(t, runDiff, req(t, dir, map[string]any{"commit": first.String()}))
	if strings.Contains(plain, "is a merge") {
		t.Errorf("a commit that is no merge says it is one:\n%s", plain)
	}
}
