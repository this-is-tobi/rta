package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// workingTree opens proj, a checkout, as r's surface opens it, and hands back
// the directory its working tree is read from, with what closes it.
func workingTree(t *testing.T, r plugin.Request) boundDir {
	t.Helper()
	repo, done, verr := openRepo(context.Background(), r)
	if verr != nil {
		t.Fatal(verr)
	}
	t.Cleanup(done)
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	tree, err := worktreeDir(r, wt)
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

// A directory of the working tree, swapped for a link out of the roots after
// the diff looked at what is in it and before it read it: a link there was
// read through, its text diffed as the link's, and a file's content, where
// the file behind the link was the one looked at. At a terminal the working
// tree is read by name, as git reads it.
func TestAWorkingTreeDirectorySwappedAfterTheDiffLookedIsNotReadThroughTheLink(t *testing.T) {
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSecret, filepath.Join(outside, "notes", "l")); err != nil {
		t.Fatal(err)
	}
	for _, surface := range []string{"MCP", "terminal"} {
		root := t.TempDir()
		proj := filepath.Join(root, "proj")
		repoAt(t, root, proj)
		if err := os.MkdirAll(filepath.Join(proj, "notes"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("inside", filepath.Join(proj, "notes", "l")); err != nil {
			t.Fatal(err)
		}
		r := req(t, proj, nil)
		if surface == "MCP" {
			g, err := pathguard.New(root)
			if err != nil {
				t.Fatal(err)
			}
			r = overMCP(t, proj, g.Derived, nil)
		}
		tree := workingTree(t, r)
		disk := newWorkingFiles(tree).at("notes/l")
		if disk == nil {
			t.Fatal("the link to diff is not there")
		}
		linkOut(t, filepath.Join(proj, "notes"), filepath.Join(outside, "notes"))()
		content, _, err := readWorktreeEntry(tree, "notes/l", disk, r.LinkTarget)
		read := strings.Contains(content, outsideSecret)
		if surface == "MCP" && read {
			t.Errorf("over MCP the diff read a link out of the roots: %q", content)
		}
		if surface == "terminal" && (!read || err != nil) {
			t.Errorf("at a terminal the diff reads by name, as git does: %q, %v", content, err)
		}
	}
}

// And the status's look at what kind each changed path is on disk, which a
// listing of its directory answers: swapped out, it listed the directory the
// link led to, and a path's kind there made it T or M.
func TestAWorkingTreeDirectorySwappedBeforeTheStatusListsItIsNotListedThroughTheLink(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "notes"), "a.txt", outsideSecret)
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	repoAt(t, root, proj)
	writeFile(t, filepath.Join(proj, "notes"), "a.txt", "inside")
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	tree := workingTree(t, overMCP(t, proj, g.Derived, nil))
	linkOut(t, filepath.Join(proj, "notes"), filepath.Join(outside, "notes"))()
	budget := &statusBudget{ctx: context.Background(), deadline: time.Now().Add(time.Minute)}
	if kind, ok := entryKind(map[string]map[string]os.FileMode{}, tree, "notes/a.txt", budget); ok {
		t.Errorf("over MCP the status listed a directory a link out of the roots leads to: %v", kind)
	}
}

// The hooks directory, swapped for a link out of the roots the moment the gate
// has judged it: the listing named every hook in the directory the link led
// to.
func TestAHooksDirectorySwappedAfterItIsJudgedIsNotListedThroughTheLink(t *testing.T) {
	outside := t.TempDir()
	writeExecutable(t, outside, "pre-commit-"+outsideSecret)
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	repoAt(t, root, proj)
	hooks := filepath.Join(proj, ".git", "hooks")
	writeExecutable(t, hooks, "pre-commit")
	gate := swapOnce(t, root, hooks, linkOut(t, hooks, outside))
	v, err := runHooks(context.Background(), overMCP(t, proj, gate, nil))
	if answered := fmt.Sprintf("%+v %v", v, err); strings.Contains(answered, outsideSecret) {
		t.Errorf("git.hooks listed a directory a link swapped in after the gate judged it leads to: %s", answered)
	}
	if _, err := os.Lstat(hooks + ".aside"); err != nil {
		t.Fatalf("the swap was never made, so the test proves nothing: %v", err)
	}
}

// A hook that is a link is judged by what it leads to, and the directory that
// is in, swapped for a link out of the roots the moment the gate has judged
// the hook's far end, was looked through: the status said what kind of thing
// was at the same name outside, a directory git fails on or nothing at all.
func TestAHookLinksFarEndSwappedAfterItIsJudgedIsNotLookedAtThroughTheLink(t *testing.T) {
	status := func(outsideKind string) string {
		outside := t.TempDir()
		if outsideKind == "directory" {
			if err := os.MkdirAll(filepath.Join(outside, "pc"), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		root := t.TempDir()
		proj := filepath.Join(root, "proj")
		repoAt(t, root, proj)
		scripts := filepath.Join(root, "scripts")
		writeExecutable(t, scripts, "pc")
		hooks := filepath.Join(proj, ".git", "hooks")
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(scripts, "pc"), filepath.Join(hooks, "pre-commit")); err != nil {
			t.Fatal(err)
		}
		gate := swapOnce(t, root, filepath.Join(scripts, "pc"), linkOut(t, scripts, outside))
		tbl := table(t, runHooks, overMCP(t, proj, gate, nil))
		if _, err := os.Lstat(scripts + ".aside"); err != nil {
			t.Fatalf("the swap was never made, so the test proves nothing: %v", err)
		}
		return rowFor(t, tbl, "Name", "pre-commit")[1]
	}
	if dir, none := status("directory"), status("nothing"); dir != none {
		t.Errorf("a hook's far end swapped out of the roots answers by what is there: %s where a directory is, "+
			"%s where nothing is", dir, none)
	}
}

// A file the repository's config includes, and the file its
// core.excludesFile names, each judged by the gate and read by name: the
// directory holding it swapped for a link out of the roots the moment the
// gate had judged it, git.config showed what the file of the same name there
// set, and the status applied its patterns, which of the files it lists
// saying what they were.
func TestAFileTheConfigNamesSwappedAfterItIsJudgedIsNotReadThroughTheLink(t *testing.T) {
	machineConfig(t, "")
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "cfg"), "inc.cfg", "[x]\n\tplanted = "+outsideSecret+"\n")
	writeFile(t, filepath.Join(outside, "cfg"), "ignores", "*.txt\n")
	for _, c := range []struct {
		name, file, setting string
		run                 plugin.Handler
		leaked              func(v any) bool
	}{
		{"an include", "inc.cfg", "[include]\n\tpath = %s\n", runConfig,
			func(v any) bool { return strings.Contains(fmt.Sprint(v), outsideSecret) }},
		{"core.excludesFile", "ignores", "[core]\n\texcludesFile = %s\n", runStatus,
			func(v any) bool { return !strings.Contains(fmt.Sprint(v), "new.txt") }},
	} {
		root := t.TempDir()
		proj := filepath.Join(root, "proj")
		repoAt(t, root, proj)
		writeFile(t, proj, "new.txt", "untracked\n")
		cfg := filepath.Join(root, "cfg")
		writeFile(t, cfg, "inc.cfg", "[x]\n\tplanted = inside\n")
		writeFile(t, cfg, "ignores", "nothing-here\n")
		content, err := os.ReadFile(filepath.Join(proj, ".git", "config"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, proj, ".git/config", string(content)+fmt.Sprintf(c.setting, filepath.Join(cfg, c.file)))
		gate := swapOnce(t, root, filepath.Join(cfg, c.file), linkOut(t, cfg, filepath.Join(outside, "cfg")))
		v, err := c.run(context.Background(), overMCP(t, proj, gate, nil))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if c.leaked(v) {
			t.Errorf("%s was read through a link swapped in after the gate judged it: %+v", c.name, v)
		}
		if tbl, _ := v.(view.Table); len(tbl.Warnings) != 1 {
			t.Errorf("%s: warnings = %+v, want the one file that was not read named", c.name, tbl.Warnings)
		}
		if _, err := os.Lstat(cfg + ".aside"); err != nil {
			t.Fatalf("%s: the swap was never made, so the test proves nothing: %v", c.name, err)
		}
	}

	// And an excludes file whose directory is not there is passed over, as
	// git passes over one that is not there, and not named as unread.
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	repoAt(t, root, proj)
	writeFile(t, proj, "new.txt", "untracked\n")
	content, err := os.ReadFile(filepath.Join(proj, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, proj, ".git/config", string(content)+"[core]\n\texcludesFile = "+
		filepath.Join(root, "nowhere", "ignores")+"\n")
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if tbl := table(t, runStatus, overMCP(t, proj, g.Derived, nil)); len(tbl.Warnings) != 0 ||
		!strings.Contains(fmt.Sprint(tbl.Rows), "new.txt") {
		t.Errorf("an excludes file in a directory that is not there: %+v", tbl)
	}
}
