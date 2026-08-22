package git

import (
	"context"
	"testing"
)

func TestBlameAttributesEachLineToTheCommitThatIntroducedIt(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "line one\nline two\n", "first commit")
	commitFile(t, repo, dir, "a.txt", "line one\nline two\nline three\n", "second commit")

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

	_, err := runBlame(context.Background(), req(t, dir, map[string]any{"file": "nope.txt"}))
	if err == nil {
		t.Fatal("expected an error blaming a file that was never committed")
	}
}
