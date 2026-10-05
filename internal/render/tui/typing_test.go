package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func typeWord(t *testing.T, m Model, word string) Model {
	t.Helper()
	for _, r := range word {
		m = press(t, m, string(r))
	}
	return m
}

// The landing screen looks like a box and used to answer like a keyboard: a
// word typed into it opened the plugin inventory on its `p`, the theme editor
// on its `t`, and ate the rest as commands there.
func TestAWordTypedOnTheLandingScreenIsSearchedForNotRunAsKeys(t *testing.T) {
	for _, word := range []string{"password", "trust", "dns", "hosts list", "who uses port"} {
		base, _ := realModel(t, 120, 40)
		m := typeWord(t, base, word)
		if m.mode != modeDashboard {
			t.Errorf("typing %q left the dashboard for %v", word, m.mode)
		}
		if !m.searchEditing || m.query != word {
			t.Errorf("typing %q left the bar editing=%v with %q in it", word, m.searchEditing, m.query)
		}
		if m.themeForm != nil {
			t.Errorf("typing %q opened the theme editor", word)
		}
	}
	base, _ := realModel(t, 120, 40)
	m := typeWord(t, base, "password")
	found := false
	for _, c := range m.searchResults() {
		found = found || c.ID == "gen.password"
	}
	if !found {
		t.Error("the search for \"password\" does not find gen.password")
	}
}

// The letters go back to the commands the moment there is a tile to give them
// to, and the footer says so in the same keystroke.
func TestAnArrowHandsTheLettersBackToTheTilesAndTheFooterFollows(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	if bar := footerOf(m.dashboardView(), footerMaxLines); !strings.Contains(bar, "type to search") {
		t.Errorf("the bar holding the selection does not say that typing searches:\n%s", bar)
	}
	m = press(t, m, "down")
	if m.selected != 1 {
		t.Fatalf("down selected %d, want the first tile", m.selected)
	}
	bar := footerOf(m.dashboardView(), footerMaxLines)
	if strings.Contains(bar, "type to search") || !strings.Contains(bar, "p plugins") {
		t.Errorf("a tile is selected and the footer still teaches the bar's keys:\n%s", bar)
	}
	if opened := press(t, m, "p"); opened.mode != modePlugins {
		t.Errorf("p on a tile opened %v, want the plugin inventory", opened.mode)
	}
	if back := press(t, m, "q"); back.mode != modeDashboard || back.query != "" {
		t.Errorf("q on a tile typed into the bar: mode %v query %q", back.mode, back.query)
	}
}

// Whatever cannot be a letter of a capability search stays a command, and
// ctrl+c quits from anywhere.
func TestTheBarKeepsTheFewKeysThatCannotBeLettersOfAQuery(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	if m := press(t, base, "/"); !m.searchEditing || m.query != "" {
		t.Errorf("/ left editing=%v query %q, want the bar open and empty", m.searchEditing, m.query)
	}
	if m := press(t, base, ":"); m.mode != modeBrowse {
		t.Errorf(": opened %v, want the catalogue", m.mode)
	}
	if m := press(t, base, "+"); m.mode != modeBrowse {
		t.Errorf("+ opened %v, want the catalogue to add from", m.mode)
	}
	if m := press(t, base, "?"); !m.help {
		t.Error("? did not open the key list")
	}
	if m := press(t, base, "enter"); !m.searchEditing {
		t.Error("enter did not give the bar the keyboard")
	}
	if _, cmd := base.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Error("ctrl+c did not quit from the landing screen")
	}
}

// A leading space is how somebody opens a box, and a query that begins with
// one only has to be trimmed again.
func TestASpaceOpensTheBarWithoutBeingTyped(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	m := press(t, base, " ")
	if !m.searchEditing || m.query != "" {
		t.Errorf("space left editing=%v query %q, want the bar open and empty", m.searchEditing, m.query)
	}
}

// With nothing under the bar there is nothing to hand the letters to, and
// `p` is the key the dashboard's own note says brings the hidden tiles back.
func TestADashboardWithNoTileKeepsItsLettersAsCommands(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m.tiles = m.tiles[:1]
	if opened := press(t, m, "p"); opened.mode != modePlugins {
		t.Errorf("p on a dashboard with no tile opened %v, want the plugin inventory", opened.mode)
	}
	if bar := footerOf(m.dashboardView(), footerMaxLines); strings.Contains(bar, "type to search") {
		t.Errorf("the footer teaches typing where typing opens panes:\n%s", bar)
	}
}

func pasted(t *testing.T, m Model, content string) Model {
	t.Helper()
	out, _ := m.Update(tea.PasteMsg{Content: content})
	return out.(Model)
}

// The terminal delivers a paste as one event, which no key handler sees, so a
// capability ID copied from a message was dropped by the box that takes it typed.
func TestAPasteOnTheLandingScreenIsASearch(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	m := pasted(t, base, "gen.password")
	if !m.searchEditing || m.query != "gen.password" {
		t.Fatalf("the paste left editing=%v with %q in the bar, want gen.password", m.searchEditing, m.query)
	}
	m = pasted(t, m, "x\ny")
	if m.query != "gen.passwordx y" {
		t.Errorf("a second paste made %q, want it appended with the line break collapsed", m.query)
	}
}

func TestAPasteIsCleanedAndBoundedBeforeItIsDrawnBack(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	m := pasted(t, base, "dns\x1b[31m\a\tmx\r\n")
	if m.query != "dns[31m mx" {
		t.Errorf("the paste made %q, want the escape and the bell dropped and the tab a space", m.query)
	}
	long := pasted(t, base, strings.Repeat("a", 500))
	if got := len([]rune(long.query)); got != maxPastedQuery {
		t.Errorf("a 500-character paste made a %d-character query, want %d", got, maxPastedQuery)
	}
}

func TestAPasteGoesToTheTileOrFormThatHasTheKeyboard(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	selected := press(t, base, "down")
	if m := pasted(t, selected, "dns"); m.query != "" || m.searchEditing {
		t.Errorf("a paste with a tile selected started a search: editing=%v %q", m.searchEditing, m.query)
	}
}
