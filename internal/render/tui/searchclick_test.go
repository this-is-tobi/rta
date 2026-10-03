package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Clicking a tile while the search box is open spends the query, as a launch
// from the box does. It used to leave both standing: back on the dashboard the
// box still held the keyboard with the old query in it, so `q` typed a letter
// where the footer promised a quit.
func TestClickingATileLeavesTheSearchBoxSpent(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	if len(m.tiles) < 2 {
		t.Fatalf("need a tile besides the search bar, got %d", len(m.tiles))
	}
	y := -1
	for row := range m.height {
		if m.tileAt(5, row) == 1 {
			y = row
			break
		}
	}
	if y < 0 {
		t.Fatal("no cell on screen maps to the first tile")
	}
	m = press(t, m, "/")
	m = press(t, press(t, m, "a"), "b")
	if !m.searchEditing || m.query != "ab" {
		t.Fatalf("the box did not take the query: editing=%v query=%q", m.searchEditing, m.query)
	}
	next, _ := m.Update(tea.MouseClickMsg{X: 5, Y: y, Button: tea.MouseLeft})
	got := next.(Model)
	if got.searchEditing || got.query != "" {
		t.Errorf("after the click the box is still open: editing=%v query=%q", got.searchEditing, got.query)
	}
}
