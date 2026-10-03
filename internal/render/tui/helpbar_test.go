package tui

import (
	"strings"
	"testing"
)

// A footer too short for everything still says how to leave. Esc and q ranked
// below the screen's own actions and navigation, so at fifty columns the plugin
// pane's bar was full before it reached them and dropped both, ending in "…"
// on a screen that did not say how to get out of it.
func TestANarrowFooterAlwaysSaysHowToLeave(t *testing.T) {
	for _, width := range []int{60, 50, 40, 30} {
		m, _ := realModel(t, width, 30)
		for name, bar := range map[string]string{
			"dashboard": footerOf(m.dashboardView(), footerMaxLines),
			"plugins":   footerOf(m.pluginsView(), footerMaxLines),
		} {
			if !strings.Contains(bar, "q quit") {
				t.Errorf("%s at %d columns does not say how to quit:\n%s", name, width, bar)
			}
		}
		if bar := footerOf(m.pluginsView(), footerMaxLines); !strings.Contains(bar, "esc back") {
			t.Errorf("plugins at %d columns does not say how to go back:\n%s", width, bar)
		}
	}
}

// And the way to the rest of the keys outlasts the arrangement keys: a bar that
// ended in "…" at forty columns left out the one key that lists what it
// dropped.
func TestANarrowDashboardFooterKeepsHelp(t *testing.T) {
	for _, width := range []int{60, 50, 40} {
		m, _ := realModel(t, width, 30)
		if bar := footerOf(m.dashboardView(), footerMaxLines); !strings.Contains(bar, "? help") {
			t.Errorf("the dashboard at %d columns dropped \"? help\":\n%s", width, bar)
		}
	}
}

// A confirmation outlasts how to leave, so a save is never silent, and both
// fit: the flash is cut to a line and the bar has two.
func TestANarrowFooterKeepsTheFlashAndHowToLeave(t *testing.T) {
	const long = "saved profile proj1-staging — covers pg, s3 and vault, and the switch lapses in 8h"
	for _, width := range []int{80, 60, 40} {
		m, _ := realModel(t, width, 30)
		m.flash = long
		bar := footerOf(m.dashboardView(), footerMaxLines)
		if !strings.Contains(bar, "✓ saved profile") || !strings.Contains(bar, "q quit") {
			t.Errorf("at %d columns the flash and \"q quit\" did not both survive:\n%s", width, bar)
		}
	}
}
