package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/go-git/go-git/v5"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **A path the gate judged is a name, and a caller who can write inside the
// roots can change what the name leads to before it is opened.** Each test
// here swaps a directory or a file for a link out of the roots at the one
// moment that matters, the instant the gate has judged the path, which a
// caller racing the call hits sooner or later and a test hits every time: the
// gate is wrapped so that judging the watched path makes the swap. What was
// swapped out holds a line nothing inside the roots does, and no answer may
// hold it.

// swapOnce is a gate over root that swaps what is at watch the first time it
// judges watch, after judging it, the way a caller racing the call would.
func swapOnce(t *testing.T, root, watch string, swap func()) func(field, path string) (string, *view.Error) {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	watched := realPath(watch)
	var once sync.Once
	return func(field, path string) (string, *view.Error) {
		judged, verr := g.Derived(field, path)
		if verr == nil && judged == watched {
			once.Do(swap)
		}
		return judged, verr
	}
}

// linkOut replaces what is at name with a link to target, moving it aside
// rather than removing it, as a caller swapping it back would.
func linkOut(t *testing.T, name, target string) func() {
	t.Helper()
	return func() {
		if err := os.Rename(name, name+".aside"); err != nil {
			t.Error(err)
			return
		}
		if err := os.Symlink(target, name); err != nil {
			t.Error(err)
		}
	}
}

// overMCP is a call over MCP to path, confined by gate.
func overMCP(t *testing.T, path string, gate func(string, string) (string, *view.Error),
	values map[string]any,
) plugin.Request {
	t.Helper()
	return req(t, path, values).WithConfinement(gate).WithSurface(plugin.SurfaceMCP)
}

// secretRepo is a repository outside every root this file draws, whose one
// commit, file and config hold outsideSecret.
func secretRepo(t *testing.T) string {
	t.Helper()
	dir, repo := testRepo(t)
	commitFile(t, repo, dir, "creds.txt", outsideSecret+"\n", "commit "+outsideSecret)
	cfg, err := repo.Config()
	if err != nil {
		t.Fatal(err)
	}
	cfg.Raw.Section("user").SetOption("name", outsideSecret)
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	return dir
}

const outsideSecret = "OUTSIDE-THE-ROOTS-1f2e"

// swapped is a repository made inside root for a test to swap a part of: the
// path a call names, the path whose judgement is the moment of the swap, and
// what is swapped for a link to where, out of the roots.
type swapped struct{ path, watch, name, target string }

// openers is every capability that opens a repository, by name.
var openers = map[string]plugin.Handler{
	"log": runLog, "diff": runDiff, "status": runStatus, "config": runConfig,
	"branches": runBranches, "overview": runOverview, "hooks": runHooks, "remotes": runRemotes,
}

// neverLedOut runs every capability that opens a repository over MCP on a
// repository layout makes, each on one of its own, swapping its part at the
// moment the layout says, and fails where an answer or an error holds what
// the link led to.
func neverLedOut(t *testing.T, layout func(t *testing.T, root, outside string) swapped) {
	t.Helper()
	outside := secretRepo(t)
	for name, h := range openers {
		root := t.TempDir()
		s := layout(t, root, outside)
		gate := swapOnce(t, root, s.watch, linkOut(t, s.name, s.target))
		values := map[string]any{"limit": defaultLogLimit}
		if name == "diff" {
			values["commit"] = "HEAD"
		}
		v, err := h(context.Background(), overMCP(t, s.path, gate, values))
		if answered := fmt.Sprintf("%+v %v", v, err); strings.Contains(answered, outsideSecret) {
			t.Errorf("%s read through a link swapped in after the gate judged the path: %s", name, answered)
		}
		if _, err := os.Lstat(s.name + ".aside"); err != nil {
			t.Fatalf("%s: the swap was never made, so the test proves nothing: %v", name, err)
		}
	}
}

// repoAt makes a repository with one commit at dir, whose objects/info/
// alternates names a directory of objects elsewhere under root: the gate
// judges each entry after it has judged every directory the repository is
// kept in, which is the moment each test here swaps one of them, a moment go-
// git's opener leaves open whatever the swap.
func repoAt(t *testing.T, root, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	commitFile(t, repo, dir, "a.txt", "inside\n", "inside the roots")
	if err := os.MkdirAll(sharedObjects(root), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, ".git", "objects", "info"), "alternates", sharedObjects(root)+"\n")
}

// sharedObjects is the directory of objects repoAt's alternates names.
func sharedObjects(root string) string { return filepath.Join(root, "shared", "objects") }

func TestAGitDirectorySwappedAfterItIsJudgedIsNotReadThroughTheLink(t *testing.T) {
	neverLedOut(t, func(t *testing.T, root, outside string) swapped {
		proj := filepath.Join(root, "proj")
		repoAt(t, root, proj)
		return swapped{path: proj, watch: sharedObjects(root), name: filepath.Join(proj, ".git"),
			target: filepath.Join(outside, ".git")}
	})
}

// The whole checkout, its working tree with its git directory.
func TestACheckoutSwappedAfterItIsJudgedIsNotReadThroughTheLink(t *testing.T) {
	neverLedOut(t, func(t *testing.T, root, outside string) swapped {
		proj := filepath.Join(root, "proj")
		repoAt(t, root, proj)
		return swapped{path: proj, watch: sharedObjects(root), name: proj, target: outside}
	})
}

// The git directory a .git file names, as a linked worktree's and a
// submodule's do.
func TestAGitDirectoryAPointerNamesSwappedAfterItIsJudgedIsNotReadThroughTheLink(t *testing.T) {
	neverLedOut(t, func(t *testing.T, root, outside string) swapped {
		main := filepath.Join(root, "main")
		repoAt(t, root, main)
		gitDir := filepath.Join(root, "store", "proj.git")
		if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(main, ".git"), gitDir); err != nil {
			t.Fatal(err)
		}
		writeFile(t, main, ".git", "gitdir: "+gitDir+"\n")
		return swapped{path: main, watch: sharedObjects(root), name: gitDir, target: filepath.Join(outside, ".git")}
	})
}

// The common directory a linked worktree's git directory names, which keeps
// its objects, refs and config.
func TestACommonDirectorySwappedAfterItIsJudgedIsNotReadThroughTheLink(t *testing.T) {
	neverLedOut(t, func(t *testing.T, root, outside string) swapped {
		main := filepath.Join(root, "main")
		repoAt(t, root, main)
		common := filepath.Join(main, ".git")
		wt := filepath.Join(root, "wt")
		own := filepath.Join(root, "own")
		head, err := os.ReadFile(filepath.Join(common, "HEAD"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, own, "HEAD", string(head))
		writeFile(t, own, "commondir", common+"\n")
		writeFile(t, own, "gitdir", filepath.Join(wt, ".git")+"\n")
		writeFile(t, wt, ".git", "gitdir: "+own+"\n")
		return swapped{path: wt, watch: sharedObjects(root), name: common, target: filepath.Join(outside, ".git")}
	})
}
