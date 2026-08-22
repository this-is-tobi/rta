package git

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func kvValue(t *testing.T, kv view.KeyValue, key string) string {
	t.Helper()
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q pair in %v", key, kv.Pairs)
	return ""
}

func TestOverviewCompactReportsBranchStatusAndLastCommit(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	if got := kvValue(t, kv, "branch"); got != "master" {
		t.Errorf("branch = %q, want %q", got, "master")
	}
	if got := kvValue(t, kv, "working tree"); got != "clean" {
		t.Errorf("working tree = %q, want %q", got, "clean")
	}
	if got := kvValue(t, kv, "last commit"); got == "" {
		t.Error("last commit is empty")
	}
}

func TestOverviewCompactReportsChangedPathCount(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	writeFile(t, dir, "a.txt", "v2\n")

	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	if got := kvValue(t, kv, "working tree"); got != "1 path(s) changed" {
		t.Errorf("working tree = %q, want %q", got, "1 path(s) changed")
	}
}

func TestOverviewDetailedComposesStatusLogAndBranches(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runOverview(context.Background(), req(t, dir, map[string]any{"detail": true}))
	if err != nil {
		t.Fatal(err)
	}
	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("want Sections, got %s", view.TypeOf(v))
	}
	want := map[string]bool{"status": false, "log": false, "branches": false}
	for _, s := range sections.Items {
		if _, ok := want[s.ID]; ok {
			want[s.ID] = true
		}
	}
	for id, seen := range want {
		if !seen {
			t.Errorf("no %q section in %v", id, sections.Items)
		}
	}
}
