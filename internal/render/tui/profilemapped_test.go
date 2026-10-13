package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// vaultishModel is a model whose environment maps a plugin's credential onto the
// store by reference, which is the one shape this pane has to be careful about
// what it claims for.
func vaultishModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if err := config.Write(config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"dbs": {
			Secrets: map[string]string{"password": "kv:staging-db-password"},
		}}},
	}}); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "dbs", Summary: "dbs",
		Capabilities: []plugin.Capability{{
			ID: "dbs.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{
				{Name: "host", Type: plugin.String, Config: "host", Local: true},
				{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true},
			},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.profiles = m.profileRows()
	m.width, m.height = 110, 40
	return m
}

// **The pane maps; only Fill resolves.** A credential mapped onto the store was
// counted "resolved" on a machine with no store, one pane above the call that
// failed on it — and on a machine with one, "resolved" still claimed a read this
// screen never makes, since whether the entry is in a store that exists is not
// known without a passphrase.
func TestThePaneSaysACredentialIsMappedAndNeverThatItIsResolved(t *testing.T) {
	m := vaultishModel(t)
	row := m.profiles[0]

	// No store yet: the one fact about it the pane can know without unlocking it.
	got := ansi.Strip(profileDetail(row))
	if strings.Contains(got, "resolved") {
		t.Errorf("the pane calls a credential resolved: %q", got)
	}
	if !strings.Contains(got, "1 credential mapped") || !strings.Contains(got, "no store on this machine yet") {
		t.Errorf("profileDetail = %q, want the credential mapped and the missing store said", got)
	}
	if inner := ansi.Strip(connDetail(row.conns[0])); !strings.Contains(inner, "kv:staging-db-password") ||
		!strings.Contains(inner, "no store on this machine yet") {
		t.Errorf("the connection line = %q, want the reference and the missing store", inner)
	}

	// A store that exists changes what can be said, not what is claimed: the
	// reference is still only mapped.
	if err := os.WriteFile(filepath.Join(os.Getenv("RTA_DATA_DIR"), "kv.age"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.profiles = m.profileRows()
	got = ansi.Strip(profileDetail(m.profiles[0]))
	if got != "1 credential mapped" {
		t.Errorf("with a store, profileDetail = %q, want \"1 credential mapped\"", got)
	}
}
