package git

import (
	"context"
	"strings"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
)

// bareRepo builds a real bare repository on the local disk, pushed to from a
// throwaway working checkout — no network access needed, but a real enough
// fixture for both of what this file tests: opening a bare repo directly
// (git.PlainOpen, no working tree, no .git subdirectory to detect), and
// go-git's own "file" transport client, the same code path a genuine
// network clone goes through end to end once the URL differs.
func bareRepo(t *testing.T) string {
	t.Helper()
	src, repo := testRepo(t)
	commitFile(t, repo, src, "a.txt", "v1\n", "initial commit")

	bareDir := t.TempDir() + "/bare.git"
	if _, err := git.PlainInit(bareDir, true); err != nil {
		t.Fatal(err)
	}
	remote, err := repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{bareDir}})
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Push(&git.PushOptions{
		RefSpecs: []config.RefSpec{"refs/heads/master:refs/heads/master"},
	}); err != nil {
		t.Fatal(err)
	}
	return bareDir
}

// A regression test for a real bug found building this plugin: DetectDotGit
// walks upward looking for a .git entry, which a bare repository never has —
// path itself already is the git directory, not a working checkout holding
// one — so PlainOpenWithOptions{DetectDotGit: true} refused a real, valid
// bare repository outright ("repository does not exist"), even though plain
// PlainOpen opens the exact same directory fine. openRepo now falls back to
// it.
func TestOpenRepoOpensABareRepositoryDirectly(t *testing.T) {
	bareDir := bareRepo(t)

	tbl := table(t, runLog, req(t, bareDir, map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(tbl.Rows))
	}
	if !strings.Contains(tbl.Rows[0][3], "initial commit") {
		t.Errorf("message = %q", tbl.Rows[0][3])
	}
}

// cloneRepo is what openRepo hands a remote-looking path to. Exercised here
// against go-git's own "file" transport client (registered the same way
// http/ssh are, just addressed by a local path) rather than a real network
// host, so the test has no external dependency — what it proves is that the
// *git.Repository cloneRepo hands back is fully usable afterward, the same
// as one PlainOpen would have returned, not merely that Clone did not error.
func TestCloneRepoPopulatesAFullyUsableRepository(t *testing.T) {
	bareDir := bareRepo(t)

	repo, verr := cloneRepo(context.Background(), bareDir)
	if verr != nil {
		t.Fatal(verr)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	commit, err := repo.CommitObject(head.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if commit.Message != "initial commit" {
		t.Errorf("message = %q, want %q", commit.Message, "initial commit")
	}
}

// The routing decision itself, proven without depending on a real, reachable
// remote: a URL-shaped path must reach cloneRepo, never be misread as a
// bogus local path. Deliberately unreachable (port 1 is never a git server)
// rather than a real network dependency — the point is which function ran,
// not whether the clone succeeds.
func TestOpenRepoRoutesARemoteLookingPathToCloneRatherThanLocalOpen(t *testing.T) {
	_, verr := openRepo(context.Background(), "https://127.0.0.1:1/nonexistent.git")
	if verr == nil {
		t.Fatal("expected a clone failure")
	}
	if verr.Code != "git.clone.failed" {
		t.Errorf("code = %q, want git.clone.failed — a URL-shaped path must be routed to cloneRepo, "+
			"not treated as a bogus local path", verr.Code)
	}
}
