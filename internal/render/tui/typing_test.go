package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The landing screen opens with the search line selected, and a letter pressed
// there is a command exactly as it is on every tile: the search takes the
// keyboard on `/` or `enter` and not on the first letter of whatever was meant
// to be a shortcut. A cold start that swallowed `p`, `t` and `q` as the start of
// a query made the keys of the dashboard depend on where the selection happened
// to rest, and `q` quit from every tile but this one.
func TestALetterOnTheSearchLineIsACommandAndNotAQuery(t *testing.T) {
	cases := []struct {
		key  string
		want func(Model) bool
		what string
	}{
		{"p", func(m Model) bool { return m.mode == modePlugins }, "the plugin inventory"},
		{"f", func(m Model) bool { return m.mode == modeProfiles }, "the profiles pane"},
		{"b", func(m Model) bool { return m.mode == modeBrowse }, "the catalogue"},
		{"t", func(m Model) bool { return m.themeForm != nil }, "the theme form"},
	}
	for _, tc := range cases {
		base, _ := realModel(t, 120, 40)
		if base.selected != 0 {
			t.Fatalf("a cold start selects %d, want the search line", base.selected)
		}
		got := press(t, base, tc.key)
		if got.searchEditing || got.query != "" {
			t.Errorf("%s on the search line started a search: editing=%v query=%q", tc.key, got.searchEditing, got.query)
		}
		if !tc.want(got) {
			t.Errorf("%s on the search line did not open %s (mode %v)", tc.key, tc.what, got.mode)
		}
	}
	base, _ := realModel(t, 120, 40)
	if _, cmd := base.Update(keyMsg("q")); cmd == nil {
		t.Error("q on the search line did not quit, as it does on every tile")
	}
}

// What opens the search from each place the selection can be.
func TestTheSearchOpensOnSlashAnywhereAndOnEnterOnTheLine(t *testing.T) {
	base, _ := realModel(t, 120, 40)
	if m := press(t, base, "enter"); !m.searchEditing || m.query != "" {
		t.Errorf("enter on the line left editing=%v query %q, want the box open and empty", m.searchEditing, m.query)
	}
	if m := press(t, base, "/"); !m.searchEditing || m.selected != 0 || m.query != "" {
		t.Errorf("/ on the line left editing=%v selected=%d query %q", m.searchEditing, m.selected, m.query)
	}
	onATile := press(t, base, "down")
	if onATile.selected != 1 {
		t.Fatalf("down selected %d, want the first tile", onATile.selected)
	}
	if m := press(t, onATile, "/"); !m.searchEditing || m.selected != 0 || m.query != "" {
		t.Errorf("/ on a tile left editing=%v selected=%d query %q, want the line selected and open", m.searchEditing, m.selected, m.query)
	}
	if m := press(t, base, " "); m.searchEditing || m.query != "" {
		t.Errorf("space opened the search (editing=%v query %q); only / and enter do", m.searchEditing, m.query)
	}
}

// Once the box is open the letters are the query, so a word typed after `/` is a
// search over the catalogue and not a run of one-letter commands.
func TestAWordTypedAfterTheSlashIsSearchedForNotRunAsKeys(t *testing.T) {
	for _, word := range []string{"password", "trust", "dns", "hosts list", "who uses port"} {
		base, _ := realModel(t, 120, 40)
		m := typeWord(t, press(t, base, "/"), word)
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
	m := typeWord(t, press(t, base, "/"), "password")
	found := false
	for _, c := range m.searchResults() {
		found = found || c.ID == "gen.password"
	}
	if !found {
		t.Error("the search for \"password\" does not find gen.password")
	}
}

func typeWord(t *testing.T, m Model, word string) Model {
	t.Helper()
	for _, r := range word {
		m = press(t, m, string(r))
	}
	return m
}

// The bar says what the keys do from where the selection is. On the search line
// that is: enter or / searches, the letters are the panes, and nothing is
// advertised that the line cannot do.
func TestTheFooterOnTheSearchLineTeachesTheKeysItAnswers(t *testing.T) {
	m, _ := realModel(t, 160, 40)
	bar := footerOf(m.dashboardView(), footerMaxLines)
	for _, want := range []string{"enter search", "p plugins", "f profiles", "t theme", "U unlock", "q quit"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the footer on the search line lacks %q:\n%s", want, bar)
		}
	}
	for _, not := range []string{"type to search", "p f t", "move", "hide"} {
		if strings.Contains(bar, not) {
			t.Errorf("the footer on the search line still says %q, which it does not do:\n%s", not, bar)
		}
	}
	line := m.renderSearchBar(120, true)
	if !strings.Contains(line, "press / or enter to search") {
		t.Errorf("the idle line does not say how to search:\n%s", line)
	}
	if tile := press(t, m, "down"); !strings.Contains(tile.renderSearchBar(120, false), "press / to search") {
		t.Errorf("the idle line with a tile selected must say the key that works from there:\n%s", tile.renderSearchBar(120, false))
	}
}

// Moving a tile or hiding one does nothing to the search line, so the bar that
// offered both there promised two keys that were not answered.
func TestTheSearchLineAnswersNeitherMoveNorHide(t *testing.T) {
	m, _ := realModel(t, 160, 40)
	for _, key := range []string{"[", "]", "H"} {
		got := press(t, m, key)
		if got.flash != "" && got.flash != m.flash {
			t.Errorf("%s on the search line flashed %q", key, got.flash)
		}
	}
	bar := footerOf(m.dashboardView(), footerMaxLines)
	if strings.Contains(bar, "[ ]") || strings.Contains(bar, "H ") {
		t.Errorf("the footer on the search line advertises keys it does not answer:\n%s", bar)
	}
}

// With the box open the footer says what the keys do in it, and `+` adds the
// highlighted result as a tile: it says so in those words, and the border does
// not repeat the footer.
func TestTheOpenSearchBoxNamesWhatPlusDoes(t *testing.T) {
	base, _ := realModel(t, 160, 40)
	m := typeWord(t, press(t, base, "/"), "gen.password")
	bar := footerOf(m.dashboardView(), footerMaxLines)
	if !strings.Contains(bar, "add as tile") || strings.Contains(bar, "add tile") {
		t.Errorf("the footer while searching must name what + does:\n%s", bar)
	}
	box := m.renderSearchTile(120, true)
	if strings.Contains(box, "add tile") || strings.Contains(box, "add as tile") {
		t.Errorf("the box border repeats the footer's hint:\n%s", box)
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
