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

// committedByGit is a repository whose HEAD holds a file called name, committed
// by git and not by go-git, whose Add refuses a name it would not check out.
// Skipped where there is no git to do it.
func committedByGit(t *testing.T, name, content string) string {
	t.Helper()
	machineConfig(t, "")
	dir, _ := testRepo(t)
	writeFile(t, dir, name, content)
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.org", "-c", "commit.gpgsign=false", "commit", "-m", "odd name"},
	} {
		if _, ok := gitSays(t, dir, args...); !ok {
			t.Skipf("no git to commit a file called %q", name)
		}
	}
	return dir
}

// controlNamedRepo is a repository whose HEAD holds a file named with an
// escape sequence, legal to git, and unchanged since.
func controlNamedRepo(t *testing.T) string {
	t.Helper()
	return committedByGit(t, "bad\x1b[31mred.txt", "x\n")
}

// editedControlNamedRepo is controlNamedRepo with that file changed since,
// which is what puts its name in front of a reader: a status lists it, and a
// terminal would show it with the escape taken out, as a name no file has.
func editedControlNamedRepo(t *testing.T) string {
	t.Helper()
	dir := controlNamedRepo(t)
	writeFile(t, dir, "bad\x1b[31mred.txt", "x\ny\n")
	return dir
}

// A name nothing lists is no reason to refuse a repository. go-git's tree
// reader refused every name in HEAD that a checkout would not write, so one
// such file stopped the status of a clean working tree that never mentions it.
func TestAControlNamedFileNothingListsIsNotARefusal(t *testing.T) {
	dir := controlNamedRepo(t)
	if rows := table(t, runStatus, req(t, dir, nil)).Rows; len(rows) != 0 {
		t.Errorf("status lists %v, want nothing: the file is committed and unchanged", rows)
	}
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := kvValue(t, v.(view.KeyValue), "working tree"); got != "clean" {
		t.Errorf("working tree = %q, want clean", got)
	}
}

// A status that could not be read is a row saying so. The line was left out,
// and an overview with no working-tree line reads as a repository with nothing
// to report on, which is the answer for a bare one and not for a checkout.
func TestOverviewSaysWhenItCouldNotReadTheWorkingTree(t *testing.T) {
	dir := editedControlNamedRepo(t)
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := kvValue(t, v.(view.KeyValue), "working tree"); !strings.HasPrefix(got, "could not be read — ") {
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
