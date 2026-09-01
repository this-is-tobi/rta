package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// paneWithNeeds builds the pane around a plugin that declares it needs to read
// a location, so the granted/ungranted split is exercised on a real row.
func paneWithNeeds(t *testing.T, needs ...plugin.Need) Model {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil }
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{
		Name: "weather", Summary: "weather summary", Needs: needs,
		Capabilities: []plugin.Capability{
			{ID: "weather.now", Summary: "now", Safety: plugin.Read, Idempotent: true, Run: run},
		}}, registry.Origin{Path: "/usr/local/bin/rta-plugin-weather", Digest: untrustedDigest}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, m.dash, m.untrusted)
	m.width, m.height = 200, 60
	m.pluginSel = 0
	return m
}

// The pane could report a permission only while it was missing.
//
// `ungranted` made a plugin that had not been allowed say so, which was the
// point of adding it — but a plugin that *had* been allowed then rendered
// exactly like one that never asked for anything, because ungranted was empty
// in both cases and the line fell through to the plain summary. So the one
// screen whose job is "which plugins do I actually have" could not answer the
// question somebody would actually audit: what have I handed this binary?
//
// That is the same silence `waiting` and `ungranted` were each written to
// break, one permission further along.
func TestThePaneSaysWhatAPluginHasBeenAllowedToRead(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	// Before: declared, not allowed. The pane warns.
	m := paneWithNeeds(t, plugin.Need("kubeconfig"))
	row := m.plugins[0]
	if len(row.ungranted) != 1 || len(row.granted) != 0 {
		t.Fatalf("granted=%v ungranted=%v, want the need listed as ungranted", row.granted, row.ungranted)
	}

	// After: the operator allows it.
	if verr := plugintrust.Add(untrustedDigest, "weather", "/usr/local/bin/rta-plugin-weather"); verr != nil {
		t.Fatal(verr)
	}
	if verr := plugintrust.Allow(untrustedDigest, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}

	m = paneWithNeeds(t, plugin.Need("kubeconfig"))
	row = m.plugins[0]
	if len(row.ungranted) != 0 {
		t.Errorf("ungranted = %v, want none once the location is allowed", row.ungranted)
	}
	if len(row.granted) != 1 || string(row.granted[0]) != "kubeconfig" {
		t.Fatalf("granted = %v, want [kubeconfig]", row.granted)
	}
	// And it has to reach the screen, not just the struct.
	if detail := pluginDetail(row); !strings.Contains(detail, "kubeconfig") {
		t.Errorf("the row does not name what it was allowed to read: %q", detail)
	}
}

// A plugin that asked for nothing must not grow a line saying so — "allowed to
// read" with an empty list would read as a permission rather than the absence
// of one, which is the failure this change exists to fix, inverted.
func TestAPluginThatNeedsNothingSaysNothing(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := paneWithNeeds(t)
	row := m.plugins[0]
	if len(row.granted) != 0 || len(row.ungranted) != 0 {
		t.Fatalf("granted=%v ungranted=%v, want both empty", row.granted, row.ungranted)
	}
	if detail := pluginDetail(row); strings.Contains(detail, "allowed to read") {
		t.Errorf("a plugin needing nothing claims a permission: %q", detail)
	}
}

// Partly allowed is its own state and has to render as both facts at once: a
// plugin allowed one of two locations still cannot do its job, and a row
// showing only the granted half would read as ready.
func TestAPartlyAllowedPluginShowsBothHalves(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if verr := plugintrust.Add(untrustedDigest, "weather", "/usr/local/bin/rta-plugin-weather"); verr != nil {
		t.Fatal(verr)
	}
	if verr := plugintrust.Allow(untrustedDigest, []string{"kubeconfig"}); verr != nil {
		t.Fatal(verr)
	}

	m := paneWithNeeds(t, plugin.Need("kubeconfig"), plugin.Need("ssh"))
	row := m.plugins[0]
	if len(row.granted) != 1 || string(row.granted[0]) != "kubeconfig" {
		t.Errorf("granted = %v, want [kubeconfig]", row.granted)
	}
	if len(row.ungranted) != 1 || string(row.ungranted[0]) != "ssh" {
		t.Errorf("ungranted = %v, want [ssh]", row.ungranted)
	}
}
