package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// A launcher's query is spent by launching. It used to survive: open a
// capability from the bar, come back, press `/` again and every letter typed
// was appended to the old query with nothing on screen saying so until "no
// matches" — `net.dnsnote.add` was the bar's actual content in a session.
func TestLaunchingFromTheSearchBarSpendsTheQuery(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m = press(t, m, "/")
	for _, r := range "sys.cpu" {
		m = press(t, m, string(r))
	}
	if m.query != "sys.cpu" {
		t.Fatalf("query = %q after typing", m.query)
	}
	launched, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	lm := launched.(Model)
	if lm.searchEditing {
		t.Fatal("enter did not leave the search bar")
	}
	if lm.query != "" {
		t.Errorf("query = %q after launching, want it cleared", lm.query)
	}
	// Back on the dashboard, `/` opens an empty bar and typing starts fresh.
	lm.mode = modeDashboard
	again := press(t, press(t, lm, "/"), "n")
	if again.query != "n" {
		t.Errorf("query = %q after typing into a reopened bar, want just what was typed", again.query)
	}
}
