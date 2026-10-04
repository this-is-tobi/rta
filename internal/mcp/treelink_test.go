package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/builtin/all"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fs_tree lists every link in a directory with what it holds, and over MCP
// that told an agent the names outside the roots a named link is not told:
// a link to a place outside, and a link whose first hop is a link outside
// that leads back in, whichever way it is spelled. Each is shown as leading
// outside the roots, by the rule a named link is told by. A link naming a
// place under the roots, relative or absolute, keeps its target, and so does
// every link at a terminal, where the person can read their own files.
func TestTreeTellsALinksTargetOnlyWhenItNamesPlacesUnderTheRoots(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	root, outside := t.TempDir(), t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret-name")
	links := map[string]string{
		"in-rel": "file.txt",
		"in-abs": filepath.Join(resolved, "file.txt"),
		"out":    secret,
		"via":    filepath.Join(outside, "hop"),
		"climb":  filepath.Join("..", filepath.Base(outside), "hop"),
	}
	if err := os.Symlink(filepath.Join(resolved, "file.txt"), filepath.Join(outside, "hop")); err != nil {
		t.Fatal(err)
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := all.Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	s := connectWith(t, reg, Options{Paths: guard})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "fs_tree", Arguments: map[string]any{"path": root},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("fs_tree under the root was refused: %+v", res.Content)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	for _, name := range []string{outside, filepath.Join(filepath.Base(outside), "hop")} {
		if strings.Contains(text, name) {
			t.Errorf("fs_tree told the agent %s, a name outside the roots:\n%s", name, text)
		}
	}
	for _, want := range []string{"→ file.txt", "→ " + filepath.Join(resolved, "file.txt")} {
		if !strings.Contains(text, want) {
			t.Errorf("fs_tree does not show %q:\n%s", want, text)
		}
	}
	if n := strings.Count(text, "→ "+outsideRoots); n != 3 {
		t.Errorf("fs_tree showed %d links as leading outside the roots, want 3:\n%s", n, text)
	}

	c, ok := reg.Capability("fs.tree")
	if !ok {
		t.Fatal("no fs.tree")
	}
	v, err := c.Run(context.Background(), plugin.NewRequest(map[string]any{"path": root}, false, false).
		WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	shown := map[string]string{}
	for _, n := range v.(view.Tree).Roots[0].Children {
		shown[n.Label] = n.Detail
	}
	for name, target := range links {
		if shown[name] != "→ "+target {
			t.Errorf("at a terminal, %s shows %q, want its target %q", name, shown[name], target)
		}
	}
}
