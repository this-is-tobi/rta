package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/pathguard"
)

// checkPaths walks a capability's declared inputs, which is every path a
// caller *sent* and none of the paths a handler makes out of them.
//
// builtin/git makes one on every call: given a directory, it looks for the
// repository that directory belongs to by walking upward, which is what an
// operator standing in a subdirectory means and is an escape from a root. So
// the guard is carried into the request as well as applied to the arguments,
// and this is the test that the carrying happens — builtin/git's own tests
// stamp the confinement themselves and would go on passing if the bridge
// stopped doing it.
func TestAHandlerCannotDeriveAPathOutOfTheRoot(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	// A repository, with a plain directory inside it that is not one.
	outer := t.TempDir()
	repo, err := gogit.PlainInit(outer, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outer, "secret.txt"), []byte("outside the root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("secret.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("a commit the root was drawn to exclude", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Ada Lovelace", Email: "ada@example.com"},
	}); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "project")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	guard, err := pathguard.New(inner)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Paths: guard})

	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "git_log",
		Arguments: map[string]any{"path": inner},
	})
	if err != nil {
		t.Fatal(err)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !res.IsError {
		t.Fatalf("the repository above the root was read from an in-bounds path: %s", text)
	}
	if !strings.Contains(text, "core.mcp.path.outside") {
		t.Errorf("refused, but not as a root violation: %s", text)
	}
	if strings.Contains(text, "drawn to exclude") {
		t.Errorf("the outer repository's history came back in the refusal itself: %s", text)
	}
}

// The other half of the same plumbing: a URL is refused rather than quietly
// rewritten into a local path.
//
// builtin/git's path input accepts a remote URL by design and clones it in
// memory. Under a root that is an outbound request whose destination an agent
// chose, from a capability marked read with no grant in front of it — and
// before it was refused it was not even reaching the network: the guard read
// "https://host/repo.git" as a relative path, joined it to the working
// directory, and handed the handler a local path that does not exist.
func TestAURLIsNotAPath(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	guard, err := pathguard.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Paths: guard})

	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "git_log",
		Arguments: map[string]any{"path": "https://example.com/repo.git"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("a confined server accepted a URL as a path")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "core.mcp.path.remote") {
		t.Errorf("refused, but not as a remote endpoint: %s", text)
	}
}
