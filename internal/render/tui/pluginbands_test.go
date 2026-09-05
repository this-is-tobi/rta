package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/pluginhost"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The plugins pane, grouped by where its bytes came from.
//
// The pane's stated job is "which plugins do I actually have", and the half
// somebody is scanning for is nearly always the third-party one. These are
// about that half staying findable, and about the pane never claiming a
// provenance rta cannot support.

// bandPlugin is one plugin with one harmless capability.
func bandPlugin(name string) plugin.Plugin {
	return plugin.Plugin{
		Name: name, Summary: "a plugin called " + name,
		Capabilities: []plugin.Capability{{
			ID: name + ".go", Summary: "go", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}
}

const (
	storeDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	pathDigest  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	waitDigest  = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

// writeLock puts entries in rta.lock, the file plugindist.ReadLock reads.
func writeLock(t *testing.T, entries ...plugindist.LockEntry) {
	t.Helper()
	path := plugindist.LockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"plugins": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bandModel is a machine holding one plugin of every provenance there is: a
// built-in, one rta installed into its own store, one that turned up on $PATH,
// and one artifact discovery refused to launch.
func bandModel(t *testing.T, lock ...plugindist.LockEntry) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	if len(lock) > 0 {
		writeLock(t, lock...)
	}

	reg := registry.New()
	if err := reg.Register(bandPlugin("inside")); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(plugindist.StoreDir(), "managed", storeDigest, "rta-plugin-managed")
	if err := reg.RegisterFrom(bandPlugin("managed"),
		registry.Origin{Path: stored, Digest: storeDigest}); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterFrom(bandPlugin("stray"),
		registry.Origin{Path: "/usr/local/bin/rta-plugin-stray", Digest: pathDigest}); err != nil {
		t.Fatal(err)
	}

	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 110, 44
	m.untrusted = []pluginhost.Untrusted{
		{Name: "unasked", Path: "/usr/local/bin/rta-plugin-unasked", Digest: waitDigest},
	}
	m.plugins = pluginRows(reg, config.Dashboard{}, m.untrusted)
	return m
}

// groupOf finds a named row's band.
func groupOf(t *testing.T, m Model, name string) pluginGroup {
	t.Helper()
	for _, row := range m.plugins {
		if row.plugin.Name == name {
			return row.group()
		}
	}
	t.Fatalf("no row for %q", name)
	return groupBuiltin
}

// The four provenances rta can actually tell apart, told apart.
func TestEveryProvenanceRtaCanKnowGetsItsOwnBand(t *testing.T) {
	m := bandModel(t)
	for name, want := range map[string]pluginGroup{
		"inside":  groupBuiltin,
		"managed": groupManaged,
		"stray":   groupPath,
		"unasked": groupWaiting,
	} {
		if got := groupOf(t, m, name); got != want {
			t.Errorf("%s is in band %q, want %q", name, got.title(), want.title())
		}
	}
}

// An artifact rta placed and one that turned up beside it used to render as
// the same "$PATH: …" line. They are the two halves of the question this pane
// exists to answer.
func TestAnInstalledArtifactAndAStrayOneAreNotTheSameKindOfThing(t *testing.T) {
	m := bandModel(t, plugindist.LockEntry{
		Name: "managed", Digest: storeDigest, Version: "0.2.0",
		Index: "community", Signature: "verified",
	})

	var installed, stray string
	for _, row := range m.plugins {
		switch row.plugin.Name {
		case "managed":
			installed = pluginOrigin(row)
		case "stray":
			stray = pluginOrigin(row)
		}
	}
	for _, want := range []string{"installed by rta", "0.2.0", "community", "verified"} {
		if !strings.Contains(installed, want) {
			t.Errorf("the installed row says %q, missing %q", installed, want)
		}
	}
	if !strings.Contains(stray, "$PATH") {
		t.Errorf("the stray row says %q, want it named as a $PATH binary", stray)
	}
	if strings.Contains(stray, "community") || strings.Contains(stray, "0.2.0") {
		t.Errorf("the stray row borrowed provenance it has none of: %q", stray)
	}
}

// The lock is matched by digest, never by name. A record that names this
// plugin and describes different bytes is exactly what a half-finished
// upgrade, or a local build copied into the store, leaves behind — and
// attaching its version and index on the strength of the name would be rta
// reporting provenance for an artifact it did not recognise.
func TestALockRecordForOtherBytesIsNotBorrowedByName(t *testing.T) {
	m := bandModel(t, plugindist.LockEntry{
		Name: "managed", Digest: strings.Repeat("9", 64), Version: "0.2.0",
		Index: "community", Signature: "verified",
	})

	for _, row := range m.plugins {
		if row.plugin.Name != "managed" {
			continue
		}
		if row.lock != nil {
			t.Fatalf("a lock row for other bytes was attached: %+v", *row.lock)
		}
		got := pluginOrigin(row)
		if strings.Contains(got, "0.2.0") || strings.Contains(got, "community") {
			t.Errorf("the row claims provenance from a record of other bytes: %q", got)
		}
		if !strings.Contains(got, "no record of these bytes") {
			t.Errorf("the row says %q, want it to say the record does not cover this artifact", got)
		}
		return
	}
	t.Fatal("no managed row")
}

// Third-party artifacts sort together, after the built-ins they were lost
// among, and an outstanding trust decision stays last — the order `rta plugin
// list` already uses.
func TestTheThirdPartyHalfIsNotScatteredAmongTheBuiltIns(t *testing.T) {
	m := bandModel(t)
	order := make([]pluginGroup, len(m.plugins))
	for i, row := range m.plugins {
		order[i] = row.group()
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatalf("bands are interleaved: %v", order)
		}
	}
	if last := m.plugins[len(m.plugins)-1]; !last.waiting {
		t.Errorf("the last row is %q, want the artifact waiting for a decision", last.plugin.Name)
	}
}

// The rules are on the screen, each once, above the rows they head.
func TestThePaneDrawsARuleAboveEachBand(t *testing.T) {
	m := bandModel(t)
	m.mode = modePlugins
	out := plain(m.pluginsView())

	for _, g := range []pluginGroup{groupBuiltin, groupManaged, groupPath, groupWaiting} {
		title := strings.ToUpper(g.title())
		if n := strings.Count(out, title); n != 1 {
			t.Errorf("%q appears %d times, want once:\n%s", title, n, out)
		}
		if !strings.Contains(out, g.caption()) {
			t.Errorf("the %q rule carries no caption:\n%s", title, out)
		}
	}
}

// One band is not a grouping. A rule over every row on the pane separates
// nothing, and the row's own line already says "built in" — the same
// discipline that keeps an unmarked profile from printing a badge.
func TestOneProvenanceDrawsNoRulesAtAll(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	reg := registry.New()
	for _, name := range []string{"one", "two"} {
		if err := reg.Register(bandPlugin(name)); err != nil {
			t.Fatal(err)
		}
	}
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 110, 44
	m.plugins = pluginRows(reg, config.Dashboard{}, nil)
	m.mode = modePlugins

	if got := m.pluginGroups(); got != 0 {
		t.Errorf("pluginGroups = %d for a list of one provenance, want 0", got)
	}
	if out := plain(m.pluginsView()); strings.Contains(out, strings.ToUpper(groupBuiltin.title())) {
		t.Errorf("a rule was drawn over a list it separates nothing in:\n%s", out)
	}
}

// The rules cost lines, and the scroll arithmetic has to have paid for them.
// The clamp and the view read one function for exactly this reason: two that
// agree most of the time put the selection a line under the bottom edge, which
// reads as the app being broken rather than as a pane being short.
func TestTheRulesAreCountedAgainstThePanesHeight(t *testing.T) {
	m := bandModel(t)
	m.mode = modePlugins

	body := m.pluginBodyHeight()
	visible := m.visiblePlugins(body)
	if visible*pluginRowHeight+pluginGroupHeight*m.pluginGroups() > body {
		t.Fatalf("%d rows plus %d rules do not fit in %d lines",
			visible, m.pluginGroups(), body)
	}

	// The last row, on a pane too short to hold the list, still lands inside
	// the panel rather than under it.
	m.height = 20
	m.pluginSel = len(m.plugins) - 1
	m.clampPluginScroll(m.pluginBodyHeight())
	out := plain(m.pluginsView())
	if !strings.Contains(out, "UNASKED") {
		t.Errorf("the selected row is not on screen:\n%s", out)
	}
}

// Scrolling into the middle of a band does not re-announce it. A heading that
// reappears whenever you scroll is a heading that stops meaning "this is where
// it starts".
func TestScrollingIntoABandDoesNotRedrawItsRule(t *testing.T) {
	m := bandModel(t)
	m.mode = modePlugins
	m.height = 20
	m.pluginSel = len(m.plugins) - 1
	m.clampPluginScroll(m.pluginBodyHeight())

	out := plain(m.pluginsView())
	if strings.Contains(out, strings.ToUpper(groupBuiltin.title())) {
		t.Errorf("the first band's rule was redrawn after scrolling past it:\n%s", out)
	}
}
