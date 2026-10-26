package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/profile"
)

// u on the environment that is on switches it off, which is the one keystroke
// that takes every profiled reach away from every agent, and the bar called it
// "use" there as everywhere: somebody pressing it to renew the window, as `rta
// use` does, switched the environment off in the middle of the task. The key
// keeps that, and the bar says it.
func TestTheBarSaysUSwitchesOffTheEnvironmentThatIsOn(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	m.profiles = m.profileRows()
	m.mode = modeProfiles
	for i, row := range m.profiles {
		m.profileSel = i
		want := "use"
		if row.name == "staging" {
			want = "switch off"
		}
		if taught := taughtU(m); taught != want {
			t.Errorf("on %s the bar teaches u as %q, want %q", row.name, taught, want)
		}
	}

	// A window that has run out reads off in the state column, and the bar
	// says so beside it.
	at := profileIndex(t, m, "staging")
	past := time.Now().Add(-time.Minute)
	m.profiles[at].until, m.profileSel = &past, at
	if taught := taughtU(m); taught != "use" {
		t.Errorf("on a window that ran out the bar teaches u as %q, want use", taught)
	}
}

// The rows are what the pane last built, on a key, and no tick runs on it: a
// window that ran out, or `rta use --off` in another terminal, left a row
// reading on and the bar teaching u as switch off, and the press switched the
// environment on for a fresh window. A row that no longer says what is in
// force switches nothing; the rows are put right, and the next press acts on
// what the screen then shows.
func TestUOnARowThatNoLongerSaysWhatIsInForceSwitchesNothing(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	t.Cleanup(func() { _ = profile.SaveSelection(profile.Selection{}) })
	switchOn(t, &m, "staging")
	m.profiles = m.profileRows()
	m.mode = modeProfiles
	m.profileSel = profileIndex(t, m, "staging")

	// Switched off in another terminal, the row still reading on.
	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	m = send(t, m, "u")
	if got := profile.Active(); got != "" {
		t.Fatalf("u on a row still reading on switched %q on", got)
	}
	if !strings.Contains(m.flash, "not on any more") {
		t.Errorf("the flash says %q, want that staging is not on any more", m.flash)
	}
	if m.profiles[m.profileSel].active {
		t.Error("the rows still say staging is on")
	}
	if m = send(t, m, "u"); profile.Active() != "staging" {
		t.Errorf("the next u, on a row reading off, left %q on, want staging", profile.Active())
	}

	// Switched on in another terminal, the row still reading off.
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	m.profileSel = profileIndex(t, m, "prod")
	m = send(t, m, "u")
	if got := profile.Active(); got != "prod" {
		t.Errorf("u on a row still reading off switched it off: %q is on", got)
	}
	if !strings.Contains(m.flash, "switched on elsewhere") {
		t.Errorf("the flash says %q, want that prod was switched on elsewhere", m.flash)
	}
}

// taughtU is what the bar calls u.
func taughtU(m Model) string {
	for _, it := range m.footerItems(modeProfiles) {
		for _, k := range it.keys {
			if k == "u" {
				return it.label
			}
		}
	}
	return ""
}

func profileIndex(t *testing.T, m Model, name string) int {
	t.Helper()
	for i, row := range m.profiles {
		if row.name == name {
			return i
		}
	}
	t.Fatalf("no row for %s", name)
	return -1
}
