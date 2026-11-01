package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A CA kept in ~/certs was no credential location rta knew and nothing a
// strict plugin could open: `access add --path` names it for the plugin, on its
// own and without the needs it declares, and `access rm --path` takes back the
// path alone. A path the deny list keeps from every plugin is refused.
func TestAPathIsNamedForAPluginAndTakenBack(t *testing.T) {
	run := session(t, registry.New())
	certs, ssh := filepath.Join(os.Getenv("HOME"), "certs"), filepath.Join(os.Getenv("HOME"), ".ssh")
	for _, dir := range []string{certs, ssh} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	digest := strings.Repeat("d", 64)
	if verr := plugintrust.Add(digest, "lab", "/somewhere/rta-plugin-lab"); verr != nil {
		t.Fatal(verr)
	}
	SetLoadedPlugins([]*pluginhost.Client{{
		Identity: pluginhost.Identity{Path: "/somewhere/rta-plugin-lab", Digest: digest},
		Declared: plugin.Plugin{
			Name: "lab", Summary: "a lab plugin",
			Capabilities: []plugin.Capability{{ID: "lab.get", Safety: plugin.Read}},
			Needs:        []plugin.Need{plugin.NeedKubeconfig},
		},
	}})
	t.Cleanup(func() { SetLoadedPlugins(nil) })

	out, errOut, err := run("plugin", "access", "add", "lab", "--path", "~/certs")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, certs) {
		t.Errorf("the receipt does not name the path: %q", out)
	}
	if got := plugintrust.Load().Paths(digest); !slices.Equal(got, []string{certs}) {
		t.Fatalf("Paths = %v, want %s", got, certs)
	}
	if got := plugintrust.Load().Allowed(digest); len(got) != 0 {
		t.Errorf("a path alone gave the plugin what it declares too: %v", got)
	}
	tbl := needsInventory().(view.Table)
	if last := tbl.Columns[len(tbl.Columns)-1].Name; last != "Named Paths" || tbl.Rows[0][len(tbl.Rows[0])-1] != certs {
		t.Errorf("the inventory does not list the path: %v %v", tbl.Columns, tbl.Rows)
	}

	if _, _, err := run("plugin", "access", "add", "lab", "--path", ssh); err == nil ||
		!strings.Contains(err.Error(), "the ssh credential location") {
		t.Errorf("a credential location was named as a path: %v", err)
	}
	if got := plugintrust.Load().Paths(digest); !slices.Equal(got, []string{certs}) {
		t.Errorf("a refused path changed what was recorded: %v", got)
	}

	if _, errOut, err := run("plugin", "access", "rm", "lab", "--path", "~/certs"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := plugintrust.Load().Paths(digest); len(got) != 0 {
		t.Errorf("Paths after taking it back = %v", got)
	}
}
