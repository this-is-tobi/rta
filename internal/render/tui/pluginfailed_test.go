package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/pluginhost"
)

// A plugin that was approved, launched and did not come up is in the pane with
// the reason and the way out, as it is in `rta plugin list` and `rta doctor`.
// The startup line that said so is under the alternate screen by then, and a
// pane that left the row out made it look like a plugin never installed.
func failedModel(t *testing.T) Model {
	t.Helper()
	m := bandModel(t)
	m.failed = []pluginhost.Failed{{
		Name: "broken", Path: "/usr/local/bin/rta-plugin-broken", Digest: pathDigest,
		Reason: "exited with status 2: bad config\x1b]0;owned\x07 line",
		Remedy: "`rta plugin untrust broken` stops rta launching it",
	}}
	m.plugins = m.pluginInventory()
	m.mode = modePlugins
	return m
}

func TestAPluginThatDidNotStartHasARowWithItsReasonAndItsWayOut(t *testing.T) {
	m := failedModel(t)
	if got := groupOf(t, m, "broken"); got != groupFailed {
		t.Fatalf("broken is in band %q, want %q", got.title(), groupFailed.title())
	}
	screen := plain(m.pluginsView())
	for _, want := range []string{"BROKEN", "failed to start", "exited with status 2", "rta plugin untrust broken"} {
		if !strings.Contains(screen, want) {
			t.Errorf("the pane never says %q:\n%s", want, screen)
		}
	}
	if strings.Contains(screen, "owned") {
		t.Errorf("the plugin's own escape sequence reached the pane:\n%q", screen)
	}
	order := []string{"INSIDE", "MANAGED", "STRAY", "BROKEN", "UNASKED"}
	last := -1
	for _, name := range order {
		at := strings.Index(screen, name)
		if at < 0 || at < last {
			t.Errorf("%s is out of order or missing in:\n%s", name, screen)
		}
		last = at
	}
}

// Nothing of it is registered, so no key opens what is not there: each says
// how it ended instead, and none changes a file.
func TestNoKeyActsOnAPluginThatDidNotStart(t *testing.T) {
	m := failedModel(t)
	for i, row := range m.plugins {
		if row.plugin.Name == "broken" {
			m.pluginSel = i
		}
	}
	if m.plugins[m.pluginSel].usable() {
		t.Fatal("a plugin that did not start is usable")
	}
	for _, key := range []string{"t", "a", "c", "x", "enter"} {
		next := press(t, m, key)
		if next.mode != modePlugins {
			t.Errorf("%s left the pane for %v", key, next.mode)
		}
		if !strings.Contains(next.flash, "broken did not start") {
			t.Errorf("%s answered %q, want how it ended", key, next.flash)
		}
		if next.form != nil {
			t.Errorf("%s opened a form for a plugin that is not there", key)
		}
	}
}

// Reached the way a person reaches it: given at construction and opened with p.
func TestThePaneOpenedFromTheDashboardListsWhatDidNotStart(t *testing.T) {
	m, reg := realModel(t, 110, 44)
	m = New(reg, m.dash, nil, WithFailed([]pluginhost.Failed{{
		Name: "broken", Path: "/usr/local/bin/rta-plugin-broken", Digest: pathDigest, Reason: "exited with status 2",
	}}))
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 44})
	m = press(t, sized.(Model), "down")
	m = press(t, m, "p")
	if m.mode != modePlugins {
		t.Fatalf("p opened %v, want the plugins pane", m.mode)
	}
	for i, row := range m.plugins {
		if row.plugin.Name == "broken" {
			m.pluginSel = i
		}
	}
	m.clampPluginScroll(m.pluginBodyHeight())
	if out := plain(m.pluginsView()); !strings.Contains(out, "BROKEN") || !strings.Contains(out, "exited with status 2") {
		t.Errorf("the pane does not list the plugin that did not start:\n%s", out)
	}
}
