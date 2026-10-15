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

// The pane grades an environment as `rta profile list` does, from the same
// question: a credential mapped onto a store this machine has not made fails
// the first call through it, and the pane called it ok beside a third line that
// said so. The word is the first thing a person scanning the pane reads.
func TestTheProfilePaneGradesAMissingStoreAsTheListDoes(t *testing.T) {
	status := func(m Model) string { return ansi.Strip(profileStatus(m.profiles[0])) }

	t.Run("there is no store", func(t *testing.T) {
		m := vaultishModel(t)
		if got := status(m); got != "warn" {
			t.Errorf("the pane grades an environment reading a store nobody made %q, want warn", got)
		}
		// In the pane's own spelling of the command: a capability named by its
		// id, which its form fills in, and not a line to type in a shell.
		for _, want := range []string{"1 credential comes from the store (staging-db-password)",
			"no store on this machine yet", "`kv.set key=staging-db-password` creates it"} {
			if !strings.Contains(m.profiles[0].note, want) {
				t.Errorf("the note = %q, want it to say %q", m.profiles[0].note, want)
			}
		}
		// The reason is the band's note, drawn whole under its lines and wrapped to
		// the pane (band.note): the second line says which plugins the environment
		// covers, and the reason no longer shares it, where it was cut at the edge.
		if note := profileBands(m.profiles)[0].note; !strings.Contains(note, "no store on this machine yet") {
			t.Errorf("the band's note = %q, want the reason beside the word", note)
		}
	})

	t.Run("there is a store", func(t *testing.T) {
		m := vaultishModel(t)
		if err := os.WriteFile(filepath.Join(os.Getenv("RTA_DATA_DIR"), "kv.age"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		m.profiles = m.profileRows()
		if got := status(m); got != "ok" {
			t.Errorf("with a store the pane grades it %q, want ok", got)
		}
	})

	t.Run("the environment supplies the credential", func(t *testing.T) {
		m := vaultishModel(t)
		t.Setenv("RTA_PROFILE_STAGING_PASSWORD", "from-the-environment")
		m.profiles = m.profileRows()
		if got := status(m); got != "ok" {
			t.Errorf("a call never reads the store for a credential the environment gives, and the pane grades it %q", got)
		}
	})
}
