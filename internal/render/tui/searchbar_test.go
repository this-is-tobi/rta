package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// While the search box holds the keyboard the footer teaches the box's keys,
// not the dashboard's: `q quit` and `p plugins` there are letters of a query,
// so a footer offering them told somebody who had just pressed `/` that keys
// work which only type. Every single-character key the bar advertises has to
// do what it says — not extend the query — and the ones only the dashboard
// answers have to be gone.
func TestTheSearchBoxFooterOffersOnlyWhatTheBoxAnswers(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m = press(t, m, "/")
	if !m.searchEditing {
		t.Fatal("/ did not open the search box")
	}
	for _, it := range m.footerItems(modeDashboard) {
		for _, k := range it.keys {
			if len([]rune(k)) != 1 {
				continue
			}
			if got := press(t, m, k).query; got != "" {
				t.Errorf("the footer offers %q (%s) and the box types it into the query: %q", k, it.label, got)
			}
		}
	}
	bar := plainFooter(m)
	for _, gone := range []string{"plugins", "theme", "profiles", "details", "move", "hide", "browse"} {
		if strings.Contains(bar, gone) {
			t.Errorf("the footer still offers %q while the box takes the keyboard: %s", gone, bar)
		}
	}
	for _, want := range []string{"pick", "run", "add tile", "clear", "ctrl+c quit"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the footer does not offer %q while the box takes the keyboard: %s", want, bar)
		}
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c, which the footer offers as quit, does nothing in the box")
	}
	if _, quits := cmd().(tea.QuitMsg); !quits {
		t.Error("ctrl+c, which the footer offers as quit, does not quit from the box")
	}
}
