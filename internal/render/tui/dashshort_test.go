package tui

import (
	"strings"
	"testing"
)

// On a short terminal the footer is the last thing to go: it is where "q quit"
// and "? help" are. The header, the search bar and a tile row clipped to its
// borders add up to ten lines, so at ten or eleven rows the dashboard drew
// more than the terminal held and the bottom of it — the footer — was cut off.
func TestTheDashboardFooterSurvivesAShortTerminal(t *testing.T) {
	for _, height := range []int{12, 11, 10, 9, 8, 6, 4} {
		m, _ := realModel(t, 80, height)
		out := m.dashboardView()
		if got := strings.Count(out, "\n") + 1; got > height {
			t.Errorf("height %d: the dashboard is %d lines tall", height, got)
		}
		if !strings.Contains(plain(out), "quit") && height >= 9 {
			t.Errorf("height %d: the footer, and with it \"quit\", fell off the bottom:\n%s", height, plain(out))
		}
	}
}
