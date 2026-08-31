package gitclone

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// testRepo initializes a real, on-disk, non-bare repository in a temp
// directory and commits content bytes to name — enough for InMemory to
// clone from over the local (file) transport, no server needed.
func testRepo(t *testing.T, content []byte) (dir string) {
	t.Helper()
	dir = t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("file"); err != nil {
		t.Fatal(err)
	}
	author := &object.Signature{Name: "Ada Lovelace", Email: "ada@example.com", When: time.Now()}
	if _, err := wt.Commit("add file", &git.CommitOptions{Author: author}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIsRemoteSeparatesALocalPathFromAURL(t *testing.T) {
	if IsRemote("/tmp/some/checkout") {
		t.Error("a bare local path was classified as remote")
	}
	for _, url := range []string{
		"https://example.com/repo.git",
		"ssh://git@example.com/repo.git",
		"git@example.com:org/repo.git",
	} {
		if !IsRemote(url) {
			t.Errorf("IsRemote(%q) = false, want true", url)
		}
	}
}

func TestInMemoryClonesARealRepository(t *testing.T) {
	dir := testRepo(t, []byte("hello"))

	repo, verr := InMemory(context.Background(), dir, Options{})
	if verr != nil {
		t.Fatalf("cloning a real local repository failed: %v", verr)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	got, err := wt.Filesystem.Open("file")
	if err != nil {
		t.Fatalf("the checked-out worktree does not have the committed file: %v", err)
	}
	got.Close()
}

// The regression this exists to prevent: nothing bounded how much object
// data one clone could pull into memory, so a large or adversarially large
// remote repository had no ceiling below "however much RAM the machine
// has". maxObjectBytes is shrunk rather than the fixture grown to gigabytes
// — the property under test is that the cap is wired in and fires, not
// that 2 GiB is the exact right number.
func TestInMemoryRefusesARepositoryOverTheObjectSizeCap(t *testing.T) {
	old := maxObjectBytes
	maxObjectBytes = 1024 // smaller than the committed file below
	t.Cleanup(func() { maxObjectBytes = old })

	dir := testRepo(t, make([]byte, 4096))

	_, verr := InMemory(context.Background(), dir, Options{})
	if verr == nil {
		t.Fatal("a repository bigger than the object size cap was cloned without complaint")
	}
	// Not just any failure: confirming the cap is what fired, and not some
	// unrelated local-clone error that would pass this test for the wrong
	// reason.
	if !strings.Contains(verr.Message, "object size limit") {
		t.Errorf("InMemory failed with %q, want it to name the object size limit", verr.Message)
	}
}

// Under the cap, ordinary clones are unaffected — this is a ceiling, not a
// tax on every clone regardless of size.
func TestInMemoryClonesFineWellUnderTheCap(t *testing.T) {
	old := maxObjectBytes
	maxObjectBytes = 10 << 20 // 10 MiB — comfortably above a one-line file
	t.Cleanup(func() { maxObjectBytes = old })

	dir := testRepo(t, []byte("small"))

	if _, verr := InMemory(context.Background(), dir, Options{}); verr != nil {
		t.Fatalf("a repository well under the cap was refused: %v", verr)
	}
}

func TestRefuseOverMCPRefusesOnlyOnMCP(t *testing.T) {
	cli := plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceCLI)
	if RefuseOverMCP(cli, "repository") != nil {
		t.Error("a CLI call was refused as though it were MCP")
	}
	mcp := plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceMCP)
	if RefuseOverMCP(mcp, "repository") == nil {
		t.Error("an MCP call reading a remote was not refused")
	}
}
