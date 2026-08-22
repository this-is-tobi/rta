package git

import (
	"fmt"
	"testing"
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

	tbl := table(t, runLog, req(t, dir, map[string]any{"file": "b.txt", "limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 — only one commit touched b.txt", len(tbl.Rows))
	}
	if got := tbl.Rows[0][3]; got != "touches b" {
		t.Errorf("message = %q, want %q", got, "touches b")
	}
}
