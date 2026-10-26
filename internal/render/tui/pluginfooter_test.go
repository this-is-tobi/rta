package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The plugin pane's bar said "t approve" on every row, and on a plugin that is
// approved and running t takes that approval away at once: somebody pressing
// what the bar called approve, to confirm one they trust, withdrew it. On a
// built-in row, most of the pane, t and a only refused. The bar follows the
// row now: what t does there, a where there is something to allow, enter where
// there are capabilities to open.

func TestThePluginBarSaysWhatEachKeyDoesOnTheSelectedRow(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	ext := registry.Origin{Path: allowPath, Digest: untrustedDigest}
	approved := func(needs ...plugin.Need) pluginRow {
		row := needyRow(needs...)
		row.withdrawable = true
		return row
	}
	untrusted := approved("kubeconfig")
	untrusted.decided = decidedUntrust
	cases := []struct {
		name  string
		row   pluginRow
		trust string // the word t is taught with, "" for none
		allow bool
		open  bool
	}{
		{"built in", pluginRow{plugin: plugin.Plugin{Name: "kv", Needs: []plugin.Need{"kubeconfig"}}}, "", false, true},
		{"waiting to be approved", pluginRow{plugin: plugin.Plugin{Name: "weather", Needs: []plugin.Need{"kubeconfig"}},
			origin: ext, waiting: true}, "approve", false, false},
		{"approved and running", approved("kubeconfig"), "take back", true, true},
		{"approved, asking to read nothing", approved(), "take back", false, true},
		{"trusted by the system root alone", needyRow("kubeconfig"), "", true, true},
		{"failed to start", pluginRow{plugin: plugin.Plugin{Name: "weather"}, origin: ext,
			failed: &pluginhost.Failed{Reason: "exited"}}, "", false, false},
		{"approval taken back", untrusted, "", false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(registry.New(), config.Dashboard{}, nil)
			m.plugins, m.pluginSel = []pluginRow{c.row}, 0
			taught := map[string]string{}
			for _, it := range m.footerItems(modePlugins) {
				for _, k := range it.keys {
					taught[k] = it.label
				}
			}
			if got := taught["t"]; got != c.trust {
				t.Errorf("t is taught as %q, want %q", got, c.trust)
			}
			if _, ok := taught["a"]; ok != c.allow {
				t.Errorf("a taught %v, want %v", ok, c.allow)
			}
			if _, ok := taught["enter"]; ok != c.open {
				t.Errorf("enter taught %v, want %v", ok, c.open)
			}
			// And the converse, on the same row: where nothing is taught, the
			// key only says why it does nothing.
			if c.trust == "" {
				if flash := m.trustSelected(); flash == "" || flash != m.flashBad {
					t.Errorf("t untaught did %q, want a refusal", flash)
				}
			}
			if !c.allow {
				if next, _ := m.startAllowForm(c.row); next.(Model).form != nil {
					t.Error("a untaught opened a form")
				}
			}
		})
	}
}

// A plugin the system root alone trusts, every plugin of the full image among
// them, has no approval in the operator's record, and t there only refused:
// the record rta writes holds nothing for it to take, and the system root's
// is not rta's to write. The bar taught it as take back all the same.
func TestTheBarTeachesNoTakeBackOnAPluginOnlyTheSystemRootTrusts(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	system := t.TempDir()
	t.Setenv("RTA_SYSTEM_DIR", system)
	record := `{"trusted":[{"digest":"` + untrustedDigest + `","names":["weather"]}]}`
	if err := os.WriteFile(filepath.Join(system, "trusted.json"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := paneWithTrusted(t).plugins[0].trustWord(); got != "" {
		t.Errorf("on a plugin only the system root trusts, t is taught as %q, want nothing", got)
	}

	// Approved by the operator as well, t takes their approval back...
	trustWeather(t)
	m := paneWithTrusted(t)
	if got := m.plugins[0].trustWord(); got != "take back" {
		t.Errorf("on a plugin the operator approved too, t is taught as %q, want take back", got)
	}
	// ...and then the system root's trust is all that is left, which t cannot take.
	m.trustSelected()
	if got := m.plugins[0].trustWord(); got != "" {
		t.Errorf("with only the system root's trust left, t is taught as %q, want nothing", got)
	}
}
