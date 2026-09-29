package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// TestMain points the git config this package's tests read at nothing: a
// home directory of their own with no .gitconfig and no ~/.config/git/ignore,
// no system file, and none of the environment that sets config for one
// command. Every status reads the operator's config for core.excludesFile,
// so a test asserting which files are untracked passed or failed on what the
// person running it ignores everywhere. A test that needs config of its own
// writes it (machineConfig).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "rta-git-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, name := range []string{
		"GIT_CONFIG_SYSTEM", "GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "DEVELOPER_DIR",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS", "XDG_CONFIG_HOME",
	} {
		_ = os.Unsetenv(name)
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("USERPROFILE", home)
	vendorGitConfigs = []string{filepath.Join(home, "no-vendor-gitconfig")}
	systemGitConfigs = []string{filepath.Join(home, "no-system-gitconfig")}
	code := m.Run()
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

// When is set on every commit these fixtures make: go-git leaves a zero
// Signature.When alone, so a fixture without one produces commits dated 1970 —
// which reads as "57 years ago" in any view that reports an age, and as a bug
// in the view rather than in the fixture.
func signature() *object.Signature {
	return &object.Signature{Name: "Ada Lovelace", Email: "ada@example.com", When: time.Now()}
}

// testRepo initializes a real, on-disk repository in a temp directory —
// every capability here opens a real *git.Repository, so a fixture built
// from anything less would only prove the fixture, not the plugin.
func testRepo(t *testing.T) (dir string, repo *git.Repository) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo
}

// commitFile writes content to name (relative to the repo root), stages it,
// and commits it, returning the new commit's hash.
func commitFile(t *testing.T, repo *git.Repository, dir, name, content, message string) plumbing.Hash {
	t.Helper()
	return commitFileAt(t, repo, dir, name, content, message, time.Now())
}

// commitFileAt is commitFile with the commit's date under the test's control,
// for the views that report an age rather than a timestamp.
func commitFileAt(t *testing.T, repo *git.Repository, dir, name, content, message string, when time.Time) plumbing.Hash {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add(name); err != nil {
		t.Fatal(err)
	}
	author := signature()
	author.When = when
	hash, err := wt.Commit(message, &git.CommitOptions{Author: author})
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

// writeFile writes content to name (relative to dir) without staging or
// committing it — the fixture for an untracked or unstaged-modification test.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func req(t *testing.T, path string, values map[string]any) plugin.Request {
	t.Helper()
	if values == nil {
		values = map[string]any{}
	}
	if _, ok := values["path"]; !ok {
		values["path"] = path
	}
	return plugin.NewRequest(values, false, false)
}

func table(t *testing.T, h plugin.Handler, r plugin.Request) view.Table {
	t.Helper()
	v, err := h(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("want Table, got %s", view.TypeOf(v))
	}
	return tbl
}

func text(t *testing.T, h plugin.Handler, r plugin.Request) string {
	t.Helper()
	v, err := h(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	return txt.Body
}

// rowFor returns the row whose named column matches want, or fails the test.
func rowFor(t *testing.T, tbl view.Table, col, want string) []string {
	t.Helper()
	idx := -1
	for i, c := range tbl.Columns {
		if c.Name == col {
			idx = i
			break
		}
	}
	if idx < 0 {
		t.Fatalf("no %q column in %v", col, tbl.Columns)
	}
	for _, row := range tbl.Rows {
		if row[idx] == want {
			return row
		}
	}
	t.Fatalf("no row with %s = %q in %v", col, want, tbl.Rows)
	return nil
}

// gitSince is the first version of git with the behaviour a comparison with
// the git on PATH pins, and what that behaviour is, as a skip names it.
type gitSince struct{ version, does string }

// skipGitOlderThan leaves out what follows, a comparison with the git on
// PATH, where that git is older than since: this reader's own answer is
// checked before it, and on every machine.
//
// **The comparisons pin the git this reader follows, and a machine may carry
// an older one.** Debian's is 2.47.3, which reads no
// extensions.relativeWorktrees and stops at a valueless core.hooksPath that a
// later file sets again: two comparisons failed there on what git came to do
// after it, with nothing wrong in the answers compared. Left out only where
// that git is older than the one that does it, and saying so with both
// versions, so that a comparison that fails on a git new enough still fails.
func skipGitOlderThan(t *testing.T, since gitSince) {
	t.Helper()
	if have := gitOnPath(); have != "" && slices.Compare(versionNumbers(have), versionNumbers(since.version)) < 0 {
		t.Skipf("the git on PATH is %s, older than %s, the first to %s: the comparison with it is left out",
			have, since.version, since.does)
	}
}

// gitOnPath is the version of the git on PATH as `git version` prints it,
// 2.50.1 or 2.47.1.windows.1, "" where there is none to ask.
var gitOnPath = sync.OnceValue(func() string {
	out, err := exec.Command("git", "version").Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) >= 3 {
		return fields[2]
	}
	return ""
})

// versionNumbers is the first three numbers of a version as git prints one,
// each the digits a part starts with: 2.47.1.windows.1 is 2, 47 and 1.
func versionNumbers(version string) []int {
	parts := strings.Split(version, ".")
	out := make([]int, 0, 3)
	for _, part := range parts[:min(len(parts), 3)] {
		digits := len(part) - len(strings.TrimLeft(part, "0123456789"))
		n, _ := strconv.Atoi(part[:digits])
		out = append(out, n)
	}
	return out
}

// storeObject encodes one object into the repository and returns its hash.
func storeObject(t *testing.T, repo *git.Repository, enc interface {
	Encode(plumbing.EncodedObject) error
},
) plumbing.Hash {
	t.Helper()
	obj := repo.Storer.NewEncodedObject()
	if err := enc.Encode(obj); err != nil {
		t.Fatal(err)
	}
	h, err := repo.Storer.SetEncodedObject(obj)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// commitPointingAt builds a commit whose tree holds one gitlink entry —
// a submodule pinned at `at` — with the given parents. Written through the
// object API rather than by shelling out, so the fixture is the real object
// shape without needing a git binary or a second repository.
func commitPointingAt(t *testing.T, repo *git.Repository, name string, at plumbing.Hash, parents ...plumbing.Hash) plumbing.Hash {
	t.Helper()
	tree := &object.Tree{Entries: []object.TreeEntry{
		{Name: name, Mode: filemode.Submodule, Hash: at},
	}}
	treeHash := storeObject(t, repo, tree)
	sig := *signature()
	return storeObject(t, repo, &object.Commit{
		Author: sig, Committer: sig, Message: "bump " + name,
		TreeHash: treeHash, ParentHashes: parents,
	})
}

// **go-git renders nothing at all for a submodule pointer change.**
//
// Measured against a real repository: for a commit that only bumps a
// submodule, Patch comes back with one FilePatch whose Files() are both nil
// and whose Chunks() is empty, so String() is "". textOrEmpty then
// announced "no uncommitted changes" — wrong twice over on a --commit diff,
// and a commit that did change something read as one that changed nothing.
// `git show` prints `sub | 2 +-` for the same commit.
func TestDiffShowsASubmodulePointerBump(t *testing.T) {
	dir, repo := testRepo(t)
	_ = dir
	was := plumbing.NewHash("1111111111111111111111111111111111111111")
	now := plumbing.NewHash("2222222222222222222222222222222222222222")

	first := commitPointingAt(t, repo, "sub", was)
	second := commitPointingAt(t, repo, "sub", now, first)

	// The arrangement has to actually reproduce, or the test proves nothing.
	c, err := repo.CommitObject(second)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := c.Parent(0)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := parent.Patch(c)
	if err != nil {
		t.Fatal(err)
	}
	if patch.String() != "" {
		t.Skip("go-git now renders a patch for a gitlink change; this guard is no longer the one needed")
	}

	v, err := diffCommit(t.Context(), repo, second.String(), func(string) *view.Error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	txt, ok := v.(view.Text)
	if !ok {
		t.Fatalf("diff returned %s, want Text", view.TypeOf(v))
	}
	body := txt.Body
	if strings.Contains(body, "no uncommitted changes") {
		t.Fatal("a commit that moved a submodule was reported as changing nothing")
	}
	for _, want := range []string{"sub", shortHash(was), shortHash(now)} {
		if !strings.Contains(body, want) {
			t.Errorf("diff does not name %q: %q", want, body)
		}
	}
}

// **A parent that is not in the object store is a boundary, not the end of
// the history.** A shallow clone's boundary commit has real parents that
// were never fetched; dropping them silently stopped the walk there while
// still reporting a finished count, so "3 ahead" was stated about a branch
// that may be three hundred ahead. `capped` already existed for exactly
// this and only the walk limit ever set it.
func TestAheadCountIsMarkedCappedAtAShallowBoundary(t *testing.T) {
	_, repo := testRepo(t)
	missing := plumbing.NewHash("3333333333333333333333333333333333333333")

	// A commit whose parent was never fetched: the shallow boundary, exactly.
	tip := commitPointingAt(t, repo, "sub",
		plumbing.NewHash("4444444444444444444444444444444444444444"), missing)
	other := commitPointingAt(t, repo, "sub",
		plumbing.NewHash("5555555555555555555555555555555555555555"))

	if _, ok := notIn(repo, tip, other); ok {
		t.Error("a walk that stopped at an absent parent reported itself finished")
	}
	// And a history with nothing missing still reports finished, or the flag
	// stops meaning anything.
	if _, ok := notIn(repo, other, other); !ok {
		t.Error("a complete walk was reported as capped")
	}
}

// A pipe where a repository keeps a file is refused, not opened. go-git
// opened the .git file, HEAD, config and the index as it found them, and
// open(2) on a pipe with no writer blocks until one comes, which no context
// can interrupt: every git capability on such a repository never answered,
// and each call held an OS thread for good. Anything that unpacks an archive
// into the root can leave one there. And a pipe where a .git file names the
// git directory is one over MCP too, where a directory is opened as a root,
// which opens the last part of its name as whatever is there.
func TestAPipeInTheRepositoryIsRefusedRatherThanWaitedOn(t *testing.T) {
	for _, name := range []string{".git", ".git/HEAD", ".git/config", ".git/index", ".git/commondir", "gitdir"} {
		for _, surface := range []string{"terminal", "MCP"} {
			t.Run(name+" "+surface, func(t *testing.T) {
				dir, repo := testRepo(t)
				commitFile(t, repo, dir, "a.txt", "v1\n", "initial")
				target := filepath.Join(dir, filepath.FromSlash(name))
				switch name {
				case ".git":
					if err := os.RemoveAll(target); err != nil {
						t.Fatal(err)
					}
				case "gitdir":
					if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
						t.Fatal(err)
					}
					writeFile(t, dir, ".git", "gitdir: "+target+"\n")
				default:
					if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
				}
				if err := mkfifo(target); err != nil {
					t.Skipf("no named pipes here: %v", err)
				}
				r := req(t, dir, nil)
				if surface == "MCP" {
					r = guarded(t, dir, dir).WithSurface(plugin.SurfaceMCP)
				}

				done := make(chan error, 1)
				go func() {
					_, err := runStatus(context.Background(), r)
					done <- err
				}()
				select {
				case err := <-done:
					if err == nil {
						t.Error("a repository with a pipe in place of a file answered as though it were whole")
					}
				case <-time.After(10 * time.Second):
					t.Fatal("git.status is waiting on a pipe in the repository")
				}
			})
		}
	}
}
