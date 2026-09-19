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

// The substitution checkPaths makes has a second reader: a handler whose
// library wants the path in another form. builtin/git's file input reaches
// go-git, which knows a file only by its path relative to the repository —
// and the judged form the bridge hands over is absolute. Refusing an escape
// and confining an allowed path must leave the allowed one usable, or git_blame
// is a tool the catalogue lists and no agent can call.
func TestAFileInsideTheRootIsUsableByGitBlameAndLog(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Add("a.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("touches a", &gogit.CommitOptions{
		Author: &object.Signature{Name: "Ada Lovelace", Email: "ada@example.com"},
	}); err != nil {
		t.Fatal(err)
	}

	guard, err := pathguard.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Paths: guard})

	for _, tool := range []string{"git_blame", "git_log"} {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      tool,
			Arguments: map[string]any{"path": dir, "file": filepath.Join(dir, "a.txt")},
		})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*sdk.TextContent).Text
		if res.IsError {
			t.Fatalf("%s refused a file inside the root: %s", tool, text)
		}
		m, ok := res.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("%s: structured content = %T", tool, res.StructuredContent)
		}
		if rows, _ := m["rows"].([]any); len(rows) != 1 {
			t.Errorf("%s: rows = %v, want one — the file has one line and one commit", tool, m["rows"])
		}
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
