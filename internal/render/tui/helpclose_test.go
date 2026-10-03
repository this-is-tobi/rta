package tui

import (
	"strings"
	"testing"
)

// The overlay says how to close it wherever it is drawn. The line that did
// sat at the bottom of the list, which is the part of a tall overlay that a
// short terminal cuts off, so on a window under about sixteen rows the one
// screen that exists to answer "what do I press" did not say how to leave it.
func TestTheHelpOverlayAlwaysSaysHowToCloseIt(t *testing.T) {
	for _, size := range [][2]int{{100, 40}, {80, 24}, {60, 14}, {60, 9}, {40, 6}, {30, 24}} {
		for _, screen := range []mode{modeDashboard, modeBrowse, modeResult, modePlugins, modeProfiles} {
			m, _ := realModel(t, size[0], size[1])
			m.mode = screen
			out := plain(m.helpView())
			if !strings.Contains(out, "closes") {
				t.Errorf("%dx%d on the %s: the overlay does not say how to close it:\n%s",
					size[0], size[1], screenName(screen), out)
			}
			if got := strings.Count(out, "\n") + 1; got > size[1] {
				t.Errorf("%dx%d on the %s: the overlay is %d lines tall", size[0], size[1], screenName(screen), got)
			}
		}
	}
}
