package git

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestDiffOnTheRootCommitSaysSoRatherThanFailing(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "root")

	body := text(t, runDiff, req(t, dir, map[string]any{"commit": "master"}))
	if !strings.Contains(body, "root commit") {
		t.Errorf("body = %q, want a note about the root commit having no parent", body)
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

func TestDiffWorktreeOnACleanRepoSaysSo(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial")

	body := text(t, runDiff, req(t, dir, nil))
	if body != "no uncommitted changes" {
		t.Errorf("body = %q, want %q", body, "no uncommitted changes")
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
