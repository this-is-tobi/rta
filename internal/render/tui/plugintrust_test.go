package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// **The decision belongs where the evidence is.**
//
// The plugin pane showed an untrusted artifact, named its digest and its path
// — and then told you to go and type a command in another window. That was
// deliberate once, on the reasoning that approving from inside a running
// process changes a file and loads nothing, so a key would appear to work and
// not. But `rta plugin trust` does exactly the same thing and says so, and
// nobody reads that as broken; what made the in-pane version sound broken was
// the sentence it was missing.
//
// The key is also the better moment. The digest and the artifact path are on
// the screen while the decision is made, where the command shows them
// afterwards.
func TestTrustCanBeGivenFromThePaneShowingTheArtifact(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := paneWithUntrusted(t)

	if plugintrust.Load().Trusts(untrustedDigest) {
		t.Fatal("the fixture starts already trusted")
	}
	flash := m.trustSelected()

	if !plugintrust.Load().Trusts(untrustedDigest) {
		t.Errorf("the artifact was not approved: %q", flash)
	}
	// …and it says what it did not do, which is the whole objection answered.
	if !strings.Contains(flash, "restart") {
		t.Errorf("the flash does not say the plugin is not loaded yet: %q", flash)
	}
	if !strings.Contains(flash, "weather") {
		t.Errorf("the flash does not name what was approved: %q", flash)
	}
	// The row stops claiming the artifact is waiting on a decision that has
	// been taken. A pane that still said "not run" after somebody approved it
	// is the control that appears not to work.
	out := m.pluginsView()
	if !strings.Contains(out, "approved") {
		t.Errorf("the row does not reflect the decision:\n%s", out)
	}
	if strings.Contains(out, "press t to approve") {
		t.Errorf("the row still offers a decision already taken:\n%s", out)
	}
}

// Taking it back only ever narrows, and says the one thing that is not
// obvious: the plugin already running stays running until rta exits.
func TestApprovalCanBeTakenBackAndSaysWhatThatDoesNotDo(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := paneWithUntrusted(t)
	m.trustSelected()

	// Rebuild the pane the way reopening it does, so the row is trusted
	// rather than freshly decided.
	m2 := paneWithTrusted(t)
	flash := m2.trustSelected()
	if plugintrust.Load().Trusts(untrustedDigest) {
		t.Errorf("approval was not withdrawn: %q", flash)
	}
	if !strings.Contains(flash, "until rta exits") {
		t.Errorf("the flash does not say the loaded plugin keeps running: %q", flash)
	}
}

// A built-in has no artifact and no digest, so there is nothing to approve and
// the key says so rather than doing something surprising.
func TestApprovingABuiltInSaysThereIsNothingToApprove(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := paneWithUntrusted(t)
	m.pluginSel = 0 // the built-in row
	if got := m.trustSelected(); !strings.Contains(got, "built into rta") {
		t.Errorf("approving a built-in said %q", got)
	}
}

// untrustedDigest is the fixture artifact's digest, in the shape a real one
// has: 32 bytes as 64 hex characters.
var untrustedDigest = strings.Repeat("cd", 32)

// paneWithUntrusted is the plugin pane with one built-in and one artifact
// discovery found and refused to launch, cursor on the refused one.
func paneWithUntrusted(t *testing.T) Model {
	t.Helper()
	m := pluginPane(t, []pluginhost.Untrusted{{
		Name: "weather", Path: "/usr/local/bin/rta-plugin-weather", Digest: untrustedDigest,
	}})
	m.pluginSel = len(m.plugins) - 1 // untrusted rows sort last
	return m
}

// paneWithTrusted is the same pane after a restart: the artifact is approved,
// so it is a registered plugin rather than a waiting one.
func paneWithTrusted(t *testing.T) Model {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil }
	reg := registry.New()
	if err := reg.RegisterFrom(plugin.Plugin{Name: "weather", Summary: "weather summary",
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

func pluginPane(t *testing.T, waiting []pluginhost.Untrusted) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "home", Summary: "home summary",
		Capabilities: []plugin.Capability{
			{ID: "home.info", Summary: "info", Safety: plugin.Read, Idempotent: true, Run: run},
		}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil, WithUntrusted(waiting))
	m.plugins = pluginRows(reg, m.dash, m.untrusted)
	m.width, m.height = 200, 60
	return m
}
