package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The repository is a path this plugin *derives*, and the boundary can only
// check the paths a caller sends.
//
// go-git's DetectDotGit walks upward looking for a .git entry, which is
// exactly right for a person standing in a subdirectory and is an escape for
// a caller whose reach was bounded: point an MCP server at a directory that is
// not itself a checkout, and every git capability opens whichever repository
// happens to be *above* it. `~/.git` is the ordinary case — versioning one's
// dotfiles is common, and it puts every committed file's contents inside
// `git.diff`, every message inside `git.log`, and every remembered credential
// inside `git.config`. The argument passed the guard; the thing that got
// opened never went past it.

// guarded builds the request an MCP call arrives as: confined to root, asking
// about path.
func guarded(t *testing.T, root, path string) plugin.Request {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return req(t, path, nil).WithConfinement(g.Check)
}

// nested builds the shape the escape needs: a repository, and inside it a
// plain directory with no repository of its own. A root drawn around the inner
// directory is a root the outer repository is outside of.
func nested(t *testing.T) (outer, inner string) {
	t.Helper()
	outer, repo := testRepo(t)
	commitFile(t, repo, outer, "secret.txt", "the outer repository's contents\n",
		"a commit the root was drawn to exclude")
	inner = filepath.Join(outer, "project", "sub")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	return outer, inner
}

func TestARepositoryAboveTheRootIsNotOpened(t *testing.T) {
	_, inner := nested(t)
	root := filepath.Dir(inner) // …/project, itself no repository

	for name, h := range map[string]plugin.Handler{
		"log":      runLog,
		"diff":     runDiff,
		"status":   runStatus,
		"config":   runConfig,
		"branches": runBranches,
		"overview": runOverview,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := h(context.Background(), guarded(t, root, inner))
			if err == nil {
				t.Fatal("the repository above the root was opened")
			}
			if code := errCode(err); code != "core.mcp.path.outside" {
				t.Fatalf("refused as %q, want core.mcp.path.outside — the walk out of the "+
					"root has to be refused as what it is, not reported as a missing repository", code)
			}
		})
	}
}

// The whole point of walking upward survives inside the root: a caller allowed
// to read a checkout may name any directory in it.
func TestASubdirectoryOfAnAllowedRepositoryStillResolves(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "v1\n", "initial commit")
	sub := filepath.Join(dir, "deep", "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	tbl := table(t, runLog, guarded(t, dir, sub).With(map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want the repository's one commit", len(tbl.Rows))
	}
}

// And an unconfined surface is unchanged. There is a person at a terminal who
// can already read their own files, and `rta git log` from a subdirectory is
// the ordinary way to run it.
func TestAnUnconfinedCallStillWalksUpToTheRepository(t *testing.T) {
	_, inner := nested(t)

	tbl := table(t, runLog, req(t, inner, map[string]any{"limit": defaultLogLimit}))
	if len(tbl.Rows) != 1 {
		t.Fatalf("rows = %d, want the outer repository's one commit", len(tbl.Rows))
	}
}

// A URL reaching a confined handler is refused at the boundary rather than
// mangled into a local path — see pathguard.remote. Pinned from this side too,
// because this is the plugin whose path input accepts one.
func TestAURLIsRefusedRatherThanTurnedIntoALocalPath(t *testing.T) {
	root := t.TempDir()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, verr := g.Check("path", "https://example.com/repo.git"); verr == nil {
		t.Fatal("a URL was accepted as a path under a root")
	} else if verr.Code != "core.mcp.path.remote" {
		t.Fatalf("code = %q, want core.mcp.path.remote", verr.Code)
	}
}

// The boundary substitutes a Path input rather than merely approving it:
// what a handler receives is the judged form, absolute and symlink-resolved,
// whatever the caller spelled (internal/mcp's checkPaths). So a file input
// never arrives the way its help describes it, relative to the repository
// root — and that relative form is the only one go-git's tree lookup knows.
// Handed the absolute form, blame found no such file, with a hint telling the
// caller to send what it had just sent, and log's --file matched no commit: a
// well-formed empty table an agent reads as "nobody ever touched this file".
func TestAFileInsideTheRootIsHandedToGitRepositoryRelative(t *testing.T) {
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "a.txt", "a\n", "touches a")
	commitFile(t, repo, dir, "sub/b.txt", "b\n", "touches b")

	g, err := pathguard.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	file, verr := g.Check("file", filepath.Join(dir, "sub", "b.txt"))
	if verr != nil {
		t.Fatal(verr)
	}
	r := req(t, dir, map[string]any{"file": file, "limit": defaultLogLimit}).WithConfinement(g.Check)

	t.Run("blame", func(t *testing.T) {
		blame := table(t, runBlame, r)
		if len(blame.Rows) != 1 || blame.Rows[0][4] != "b" {
			t.Fatalf("blame rows = %v, want the file's one line", blame.Rows)
		}
	})
	t.Run("log", func(t *testing.T) {
		history := table(t, runLog, r)
		if len(history.Rows) != 1 || history.Rows[0][3] != "touches b" {
			t.Fatalf("log rows = %v, want the one commit that touched the file", history.Rows)
		}
	})
}

func errCode(err error) string {
	var ve *view.Error
	if errors.As(err, &ve) {
		return ve.Code
	}
	return err.Error()
}
