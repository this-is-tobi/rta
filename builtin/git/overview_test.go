package git

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
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

// "7 path(s) changed" was one number standing in for three different
// situations. What a person does next depends on which: commit it, add it, or
// ignore it.
func TestOverviewCompactSaysWhatKindOfChange(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	writeFile(t, dir, "a.txt", "v2\n")
	writeFile(t, dir, "junk.log", "output\n")

	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	kv := v.(view.KeyValue)
	if got := kvValue(t, kv, "working tree"); got != "1 modified, 1 untracked" {
		t.Errorf("working tree = %q, want the modified file and the untracked one told apart", got)
	}

	// And a staged change is its own answer, since it is the one that is ready
	// to be committed.
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("a.txt"); err != nil {
		t.Fatal(err)
	}
	v, err = runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := kvValue(t, v.(view.KeyValue), "working tree"); got != "1 staged, 1 untracked" {
		t.Errorf("working tree = %q, want the staged change named as staged", got)
	}
}

// controlNamedRepo is a repository whose HEAD holds a file named with an
// escape sequence: legal to git, and refused by go-git's tree reader, which
// stops a status or a diff of it. Skipped where there is no git to commit one.
func controlNamedRepo(t *testing.T) string {
	t.Helper()
	machineConfig(t, "")
	dir, _ := testRepo(t)
	writeFile(t, dir, "bad\x1b[31mred.txt", "x\n")
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.org", "-c", "commit.gpgsign=false", "commit", "-m", "odd name"},
	} {
		if _, ok := gitSays(t, dir, args...); !ok {
			t.Skip("no git to commit a path with a control character in its name")
		}
	}
	return dir
}

// A status that could not be read is a row saying so. The line was left out,
// and an overview with no working-tree line reads as a repository with nothing
// to report on, which is the answer for a bare one and not for a checkout.
func TestOverviewSaysWhenItCouldNotReadTheWorkingTree(t *testing.T) {
	dir := controlNamedRepo(t)
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := kvValue(t, v.(view.KeyValue), "working tree"); !strings.HasPrefix(got, "unreadable — ") {
		t.Errorf("working tree = %q, want it said that the status could not be read", got)
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

// The page a dashboard tile opens on enter, asked about a directory that is no
// repository, used to say "nothing to report" and drop the one fact every
// section had failed with: that the directory is not inside one.
func TestTheDetailPageOutsideARepositoryNamesWhy(t *testing.T) {
	dir := t.TempDir()
	_, err := runOverview(context.Background(), req(t, dir, map[string]any{"detail": true}))
	verr := view.AsError(err, "x")
	if err == nil || verr.Code != "git.notarepo" || verr.Hint == "" {
		t.Errorf("git.overview detail outside a repository = %v, want git.notarepo with its hint", err)
	}
}
