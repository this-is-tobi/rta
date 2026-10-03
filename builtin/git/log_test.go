package git

import (
	"fmt"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func TestLogListsCommitsNewestFirst(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "first commit")
	commitFile(t, repo, dir, "a.txt", "v2\n", "second commit")
	commitFile(t, repo, dir, "a.txt", "v3\n", "third commit")

	// Direct handler calls bypass plugin.Resolve, so "limit" carries no
	// Default here the way a real CLI/TUI/MCP call would fill in — an
	// explicit value is this test's job, the same convention every other
	// package's direct-call tests already use.
	tbl := table(t, runLog, req(t, dir, map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(tbl.Rows))
	}
	if got := tbl.Rows[0][3]; got != "third commit" {
		t.Errorf("newest row message = %q, want %q", got, "third commit")
	}
	if got := tbl.Rows[2][3]; got != "first commit" {
		t.Errorf("oldest row message = %q, want %q", got, "first commit")
	}
	if got := tbl.Rows[0][1]; got != "Ada Lovelace" {
		t.Errorf("author = %q, want %q", got, "Ada Lovelace")
	}
}

func TestLogRespectsLimit(t *testing.T) {
	dir, repo := testRepo(t)
	for i := 0; i < 5; i++ {
		commitFile(t, repo, dir, "a.txt", fmt.Sprintf("content %d\n", i), fmt.Sprintf("commit %d", i))
	}

	tbl := table(t, runLog, req(t, dir, map[string]any{"limit": 2}))
	if len(tbl.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(tbl.Rows))
	}
}

func TestLogFileNarrowsToCommitsThatTouchedIt(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "touches a")
	commitFile(t, repo, dir, "b.txt", "b\n", "touches b")
	commitFile(t, repo, dir, "a.txt", "a2\n", "touches a again")
	t.Chdir(dir)

	tbl := table(t, runLog, req(t, dir, map[string]any{"file": "b.txt", "limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 — only one commit touched b.txt", len(tbl.Rows))
	}
	if got := tbl.Rows[0][3]; got != "touches b" {
		t.Errorf("message = %q, want %q", got, "touches b")
	}
}

// `git log -- deploy` lists the commits that touched anything under deploy/.
// The file input compared a changed path with its name for equality, so a
// directory matched nothing and the log said "no commit reaching HEAD touched
// deploy" of a directory most of the commits had: the sentence that exists so
// an empty table is not read as a failure, saying a thing that is false.
func TestLogFileTakesADirectoryAsGitDoes(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "deploy/values.yaml", "replicas: 2\n", "set replicas")
	commitFile(t, repo, dir, "deploy/sub/extra.yaml", "x: 1\n", "add extra")
	commitFile(t, repo, dir, "deployment.md", "not under deploy/\n", "a sibling that shares the prefix")
	commitFile(t, repo, dir, "src/main.go", "package main\n", "elsewhere")
	t.Chdir(dir)

	for file, want := range map[string]string{
		"deploy":             "add extra, set replicas",
		"deploy/":            "add extra, set replicas",
		"deploy/sub":         "add extra",
		"deploy/values.yaml": "set replicas",
		"deployment.md":      "a sibling that shares the prefix",
	} {
		tbl := table(t, runLog, req(t, dir, map[string]any{"file": file, "limit": defaultLogLimit}))
		var got []string
		for _, row := range tbl.Rows {
			got = append(got, row[3])
		}
		if strings.Join(got, ", ") != want {
			t.Errorf("--file %s: commits %q, want %q", file, got, want)
		}
	}
}

// A repository with no commits yet has an empty history, as git.branches
// already answers it has no branches: the log is an empty table, where it
// failed with go-git's "reference not found". And each empty answer says
// what it means to a person, where headings with nothing under them read like
// a listing that failed.
func TestAnEmptyAnswerSaysWhatItMeans(t *testing.T) {
	dir, repo := testRepo(t)
	for name, h := range map[string]plugin.Handler{"git.log": runLog, "git.branches": runBranches} {
		if tbl := table(t, h, req(t, dir, map[string]any{"limit": defaultLogLimit})); len(tbl.Rows) != 0 || tbl.Empty == "" {
			t.Errorf("%s with no commits: rows %v, empty %q, want no rows and a sentence", name, tbl.Rows, tbl.Empty)
		}
	}
	commitFile(t, repo, dir, "empty.txt", "", "initial")
	t.Chdir(dir)
	for name, h := range map[string]plugin.Handler{"git.status": runStatus, "git.blame": runBlame} {
		if tbl := table(t, h, req(t, dir, map[string]any{"file": "empty.txt"})); len(tbl.Rows) != 0 || tbl.Empty == "" {
			t.Errorf("%s: rows %v, empty %q, want no rows and a sentence", name, tbl.Rows, tbl.Empty)
		}
	}
	tbl := table(t, runLog, req(t, dir, map[string]any{"file": "missing.txt", "limit": defaultLogLimit}))
	if len(tbl.Rows) != 0 || !strings.Contains(tbl.Empty, "missing.txt") {
		t.Errorf("git.log of a file no commit touched: rows %v, empty %q, want it named", tbl.Rows, tbl.Empty)
	}
}
