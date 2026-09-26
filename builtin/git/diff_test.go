package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/pkg/view"
)

func TestDiffCommitShowsWhatThatCommitChanged(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\n", "first")
	commitFile(t, repo, dir, "a.txt", "line one\nline two\n", "second")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(body, "+line two") {
		t.Errorf("diff does not show the added line:\n%s", body)
	}
	if !strings.Contains(body, "a.txt") {
		t.Errorf("diff does not name the changed file:\n%s", body)
	}
}

// The root commit is diffed against the empty tree, every file it holds
// shown added, as `git show` does.
//
// It answered a sentence saying it had no parent to diff against, so every
// format carried prose where a patch goes: `rta git diff --commit <root> >
// root.patch` wrote the sentence into the patch, and -o json handed it to a
// script as the diff of the one commit that changed the most.
func TestDiffOnTheRootCommitShowsEveryFileAdded(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "root")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	for _, want := range []string{"diff --git a/a.txt b/a.txt", "new file mode", "+v1"} {
		if !strings.Contains(body, want) {
			t.Errorf("root commit's diff has no %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "root commit") {
		t.Errorf("root commit's diff is prose, not a patch: %q", body)
	}
}

// A commit whose patch is empty answers an empty patch, and says why only to
// a person: the sentence was the body, so `rta git diff --commit <empty> >
// x.patch` wrote it into the patch.
func TestDiffOfACommitThatChangedNothingIsAnEmptyPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "first")
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	empty, err := wt.Commit("nothing", &git.CommitOptions{Author: signature(), AllowEmptyCommits: true})
	if err != nil {
		t.Fatal(err)
	}

	v, err := runDiff(context.Background(), req(t, dir, map[string]any{"commit": empty.String()}))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok || txt.Body != "" || !strings.Contains(txt.Empty, "changed nothing this can show") ||
		!strings.Contains(txt.Empty, shortHash(empty)) {
		t.Errorf("empty commit's diff = %#v, want an empty body and the sentence beside it", v)
	}
}

// A commit that only changes a file's mode is a patch, not the sentence: the
// sentence named a mode change as something go-git renders no patch for, and
// it renders one, `old mode` and `new mode`, as `git show` does.
func TestDiffOfAModeOnlyCommitIsAPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "run.sh", "echo hi\n", "first")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("run.sh"); err != nil {
		t.Fatal(err)
	}
	mode, err := wt.Commit("executable", &git.CommitOptions{Author: signature()})
	if err != nil {
		t.Fatal(err)
	}

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": mode.String()}))
	if !strings.Contains(body, "old mode 100644") || !strings.Contains(body, "new mode 100755") {
		t.Errorf("mode-only commit's diff = %q, want the mode change", body)
	}
}

func TestDiffWorktreeShowsUncommittedChanges(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\n", "initial")
	writeFile(t, dir, "a.txt", "line one\nline two\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+line two") {
		t.Errorf("diff does not show the uncommitted addition:\n%s", body)
	}
}

// A clean tree's diff is an empty patch, and says so only to a person.
//
// The sentence was the body, so every format carried it: `rta git diff >
// x.patch` wrote "no uncommitted changes" into the patch, and -o json handed
// a script that sentence as the diff.
func TestDiffWorktreeOnACleanRepoIsAnEmptyPatch(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	v, err := runDiff(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok || txt.Body != "" || txt.Empty != "no uncommitted changes" {
		t.Errorf("clean diff = %#v, want an empty body and the sentence beside it", v)
	}
}

func TestDiffWorktreeShowsANewUntrackedFileAsEntirelyAdded(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
	writeFile(t, dir, "new.txt", "brand new content\n")

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "+brand new content") {
		t.Errorf("diff does not show the new file's content as added:\n%s", body)
	}
	if !strings.Contains(body, "new file mode") {
		t.Errorf("diff does not mark new.txt as a new file:\n%s", body)
	}
}

func TestDiffWorktreeShowsADeletedFile(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "will be deleted\n", "initial")
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}

	body := text(t, runDiff, req(t, dir, nil))
	if !strings.Contains(body, "-will be deleted") {
		t.Errorf("diff does not show the deleted content:\n%s", body)
	}
	if !strings.Contains(body, "deleted file mode") {
		t.Errorf("diff does not mark a.txt as deleted:\n%s", body)
	}
}
