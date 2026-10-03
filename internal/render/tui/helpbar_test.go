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

// A bar that ends in "…" points at keys it cannot show, and "? help" is how
// they are found: it stays wherever the screen answers it. The plugin pane's
// bar at forty columns kept its own actions and dropped it.
func TestABarThatDroppedKeysStillOffersHelp(t *testing.T) {
	m, _ := realModel(t, 100, 40)
	for screen := modeDashboard; screen <= modeConfirm; screen++ {
		n := m
		n.mode = screen
		if !n.helpOffered(screen) {
			continue
		}
		for width := 80; width >= 30; width -= 10 {
			bar := plain(fitHintBar(width, footerMaxLines, n.footerItems(screen)...))
			if strings.Contains(bar, "…") && !strings.Contains(bar, "? help") {
				t.Errorf("%s at %d columns dropped keys and \"? help\" with them:\n%s", screenName(screen), width, bar)
			}
		}
	}
}

// While the filter box holds the keyboard the bar names what the box answers.
// It went on offering "q quit", "/ filter" and "esc back" over a box where q
// and / are letters of the query and esc clears it instead of leaving.
func TestTheCatalogueBarNamesWhatTheFilterBoxAnswers(t *testing.T) {
	m, _ := realModel(t, 100, 30)
	m.mode = modeBrowse
	m = press(t, m, "/")
	bar := footerOf(m.browseView(), footerMaxLines)
	for _, want := range []string{"esc clear", "enter apply", "ctrl+c quit"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar over the filter box lacks %q:\n%s", want, bar)
		}
	}
	for _, gone := range []string{"q quit", "/ filter", "esc back", "? help"} {
		if strings.Contains(bar, gone) {
			t.Errorf("the bar over the filter box still offers %q:\n%s", gone, bar)
		}
	}

	if m = press(t, m, "q"); m.mode != modeBrowse || m.list.FilterValue() != "q" {
		t.Errorf("q while filtering did not type into the box: mode %v, query %q", m.mode, m.list.FilterValue())
	}
}

// The arrows on the profiles panes move the cursor between the rows the other
// keys act on, so they are "select", as on every other list. They said
// "scroll", the word for a page of text with nothing to pick.
func TestTheProfilePanesCallTheArrowsSelect(t *testing.T) {
	m := profileModel(t, twoProfileConfig())
	for _, screen := range []mode{modeProfiles, modeProfilePlugins} {
		bar := plain(fitHintBar(120, footerMaxLines, m.footerItems(screen)...))
		if !strings.Contains(bar, "↑↓ select") || strings.Contains(bar, "scroll") {
			t.Errorf("%s calls its arrows something else:\n%s", screenName(screen), bar)
		}
	}
}
