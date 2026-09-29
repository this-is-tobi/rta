package fs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// bounded is the request the MCP bridge hands a handler under root.
func bounded(t *testing.T, root string, values map[string]any) plugin.Request {
	t.Helper()
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return plugin.NewRequest(values, false, false).WithSurface(plugin.SurfaceMCP).
		WithConfinement(g.Derived).WithBounds(g.Bounds()).
		WithLinkTargets(func(_, target string) string { return target })
}

// An operator who serves their home directory has put rta's own state under
// the root, and a path naming it is refused; a walk that reaches it on the
// way down was not, and listed the seal key with its size. Under a root the
// walk now stops at it and says what it left out.
func TestAWalkUnderARootLeavesRtasOwnStateOut(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, ".local", "share", "rta")
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "grants.key"), make([]byte, 3000), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".local", "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		run  plugin.Handler
		args map[string]any
	}{
		{"fs.tree", runTree, map[string]any{"path": root, "depth": 5, "all": true, "detail": true}},
		{"fs.usage", runUsage, map[string]any{"path": root, "detail": true}},
		{"fs.usage", runUsage, map[string]any{"path": root}},
	} {
		v, err := c.run(context.Background(), bounded(t, root, c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		out, err := view.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		if strings.Contains(text, "grants.key") || strings.Contains(text, "2.9 KiB") {
			t.Errorf("%s reached into rta's own state:\n%s", c.name, text)
		}
		if !strings.Contains(text, "rta's own state") {
			t.Errorf("%s did not say what it left out:\n%s", c.name, text)
		}
	}
	// At a terminal there is no root and nothing is withheld: the person
	// can read their own files.
	v, err := runTree(context.Background(), plugin.NewRequest(
		map[string]any{"path": root, "depth": 5, "all": true}, false, false).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if out, _ := view.Marshal(v); !strings.Contains(string(out), "grants.key") {
		t.Errorf("the CLI's tree withheld the operator's own files:\n%s", out)
	}
}

// A file of rta's configuration can lie under a root as well: RTA_CONFIG
// naming one in the project, and the remotes.yaml beside it. Each is refused
// by name, and a walk that reaches one names it without its size, as it
// names the directory of rta's state without what is in it.
func TestAWalkUnderARootLeavesRtasConfigurationFilesOut(t *testing.T) {
	root := t.TempDir()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_CONFIG", filepath.Join(root, "rta.yaml"))
	for name, size := range map[string]int{"rta.yaml": 3000, "remotes.yaml": 5000, "notes.txt": 1} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		run  plugin.Handler
		args map[string]any
	}{
		{"fs.tree", runTree, map[string]any{"path": root, "detail": true}},
		{"fs.usage", runUsage, map[string]any{"path": root, "detail": true}},
		{"fs.usage", runUsage, map[string]any{"path": root}},
	} {
		v, err := c.run(context.Background(), bounded(t, root, c.args))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		out, err := view.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		text := string(out)
		if strings.Contains(text, "2.9 KiB") || strings.Contains(text, "4.9 KiB") {
			t.Errorf("%s sized rta's configuration:\n%s", c.name, text)
		}
		if !strings.Contains(text, "rta's own state") {
			t.Errorf("%s did not say what it left out:\n%s", c.name, text)
		}
	}
}
