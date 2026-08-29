package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The overview answered three questions and stopped where the interesting ones
// start: am I about to push something, am I behind, is this repository
// mid-rebase. All three decide the next command, and all three were invisible
// without dropping to a shell — which is what a tile exists to save.

// fetched plants a remote-tracking ref, which is exactly what a fetch leaves
// behind: `origin/main` is a local ref pointing at what this machine last saw.
func fetched(t *testing.T, repo *git.Repository, remote, branch string, at plumbing.Hash) {
	t.Helper()
	ref := plumbing.NewHashReference(plumbing.NewRemoteReferenceName(remote, branch), at)
	if err := repo.Storer.SetReference(ref); err != nil {
		t.Fatal(err)
	}
}

// moveBranch rewinds (or advances) the checked-out branch, the way a reset
// does, so a test can produce a HEAD that is behind its upstream.
func moveBranch(t *testing.T, repo *git.Repository, to plumbing.Hash) {
	t.Helper()
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Storer.SetReference(plumbing.NewHashReference(head.Name(), to)); err != nil {
		t.Fatal(err)
	}
}

func TestTrackingReportsDriftFromTheUpstream(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	fetched(t, repo, "origin", "master", first)

	// Level with the upstream: named, and said to be level, because "which
	// remote does this push to" is half of what the line answers.
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (up to date)" {
		t.Fatalf("tracking = %q, want origin/master up to date", got)
	}

	// One local commit on top.
	second := commitFile(t, repo, dir, "b.txt", "v1\n", "second commit")
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (1 ahead)" {
		t.Fatalf("tracking = %q, want 1 ahead", got)
	}

	// And the other direction: the upstream moved on and this branch did not.
	fetched(t, repo, "origin", "master", second)
	moveBranch(t, repo, first)
	if got := overviewValue(t, dir, "tracking"); got != "origin/master (1 behind)" {
		t.Fatalf("tracking = %q, want 1 behind", got)
	}
}

// A branch with a configured upstream reports that one, whatever else is
// lying around: branch.<name>.remote is what git itself consults, and a
// repository can have several remotes with a branch of the same name on each.
func TestTrackingPrefersTheConfiguredUpstream(t *testing.T) {
	dir, repo := testRepo(t)
	first := commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	fetched(t, repo, "origin", "master", first)
	fetched(t, repo, "fork", "trunk", first)
	if err := repo.CreateBranch(&config.Branch{
		Name: "master", Remote: "fork", Merge: plumbing.ReferenceName("refs/heads/trunk"),
	}); err != nil {
		t.Fatal(err)
	}

	if got := overviewValue(t, dir, "tracking"); !strings.HasPrefix(got, "fork/trunk") {
		t.Errorf("tracking = %q, want the configured upstream", got)
	}
}

// A branch that tracks nothing says nothing. An empty line on a tile is worse
// than a missing one — it reads as a fact that failed to load.
func TestAnUntrackedBranchReportsNoTracking(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")

	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "tracking" {
			t.Errorf("tracking = %q on a branch that tracks nothing", p.Value)
		}
	}
}

// The fact most worth surfacing and the least visible: an interrupted rebase
// changes what every other command means, and nothing about a branch name, a
// commit or a file list says it is happening.
func TestAnInterruptedOperationIsReported(t *testing.T) {
	for _, c := range []struct{ marker, want string }{
		{"MERGE_HEAD", "merge in progress"},
		{"CHERRY_PICK_HEAD", "cherry-pick in progress"},
		{"REVERT_HEAD", "revert in progress"},
		{"BISECT_LOG", "bisect in progress"},
		{"rebase-merge/head-name", "rebase in progress"},
		{"rebase-apply/next", "rebase in progress"},
	} {
		t.Run(c.want, func(t *testing.T) {
			dir, repo := testRepo(t)
			commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
			marker := filepath.Join(dir, ".git", c.marker)
			if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(marker, []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := overviewValue(t, dir, "state"); got != c.want {
				t.Errorf("state = %q, want %q", got, c.want)
			}
		})
	}
}

// A rebase that stopped on a conflict has both a rebase directory and
// MERGE_HEAD. The useful word is the outer one: what to do next is continue
// the rebase, not finish the merge.
func TestARebaseStoppedOnAConflictReportsTheRebase(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	if err := os.MkdirAll(filepath.Join(dir, ".git", "rebase-merge"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "MERGE_HEAD"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := overviewValue(t, dir, "state"); got != "rebase in progress" {
		t.Errorf("state = %q, want the rebase", got)
	}
}

// An ordinary repository has no state line at all, for the same reason an
// untracked branch has no tracking line.
func TestAQuietRepositoryReportsNoState(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "state" {
			t.Errorf("state = %q on a repository doing nothing", p.Value)
		}
	}
}

// The last commit says when. A hash and a subject do not answer "is this
// repository still being worked on", which is most of why anybody looks.
func TestTheLastCommitCarriesItsAge(t *testing.T) {
	dir, repo := testRepo(t)
	commitFileAt(t, repo, dir, "a.txt", "v1\n", "initial commit",
		time.Now().Add(-3*time.Hour))
	if got := overviewValue(t, dir, "last commit"); !strings.Contains(got, "3 hours ago") {
		t.Errorf("last commit = %q, want it to say how long ago", got)
	}
}

func overviewValue(t *testing.T, dir, key string) string {
	t.Helper()
	v, err := runOverview(context.Background(), req(t, dir, nil))
	if err != nil {
		t.Fatal(err)
	}
	return kvValue(t, v.(view.KeyValue), key)
}
