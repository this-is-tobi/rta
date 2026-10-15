package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
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

// Below the room for the least a tile is drawn at, the one row the layout used to
// admit anyway was cut by the screen's own edge: a box with its top border and
// the line that says there is more, and nothing in it — and a click on it opened
// a tile nobody could see. No row is drawn there, and where the terminal has a
// line to spare it says why, which is what a dashboard that draws nothing under
// its search needs to say to not look like one that failed to load.
func TestATerminalTooSmallForATileSaysSoAndDrawsNone(t *testing.T) {
	said, silent := 0, 0
	for _, width := range []int{20, 40, 80, 120} {
		for height := 3; height <= 14; height++ {
			m, _ := realModel(t, width, height)
			out := plain(m.dashboardView())
			if got := strings.Count(out, "\n") + 1; got > height {
				t.Errorf("%dx%d: the dashboard is %d lines tall", width, height, got)
			}
			// What is left under the header, the search and the footer: the room
			// for the tiles, and the lines to spare for a note where they are not.
			room := height - lipgloss.Height(m.dashFooter()) - 1 - m.searchHeight()
			if m.tilesFit() != (room >= tileClipHeight) {
				t.Errorf("%dx%d: %d lines of room and the layout says the tiles fit: %v", width, height, room, m.tilesFit())
			}
			if room >= tileClipHeight {
				if strings.Contains(out, "terminal too small") {
					t.Errorf("%dx%d: the tiles fit and the dashboard says they do not:\n%s", width, height, out)
				}
				continue
			}
			if strings.Contains(out, "╭") {
				t.Errorf("%dx%d: a tile is drawn with no room for one:\n%s", width, height, out)
			}
			if got := m.tileAt(5, 1+m.searchHeight()); got != -1 {
				t.Errorf("%dx%d: a click where the tiles would be lands on tile %d", width, height, got)
			}
			want := tooSmallNote
			if width < len("  "+tooSmallNote) {
				want = "terminal too small"
			}
			switch {
			case room < 1:
				silent++
			case !strings.Contains(out, want):
				t.Errorf("%dx%d: there is a line to spare and the dashboard does not say %q:\n%s", width, height, want, out)
			default:
				said++
			}
		}
	}
	if said < 4 || silent < 1 {
		t.Errorf("the sizes cover %d terminals with the note and %d without room for it, want both kinds", said, silent)
	}
}

// The header's pointer to the rows off the screen is the arrow and the word, and
// the arrow alone where the word does not fit after what the header says: the
// terminal cuts a line at its edge wherever it falls, and at twenty columns the
// header read "↓ m".
func TestTheMoreHintInTheHeaderIsNeverCutThroughAWord(t *testing.T) {
	for width := 15; width <= 40; width++ {
		m, _ := realModel(t, width, 14)
		if !m.tilesFit() || m.dashRows() <= m.dashRowsVisible() {
			t.Fatalf("width %d: the test needs rows that do not fit and tiles that do", width)
		}
		header := strings.SplitN(plain(m.dashboardView()), "\n", 2)[0]
		if got := lipgloss.Width(header); got > width {
			t.Errorf("width %d: the header is %d cells wide: %q", width, got, header)
		}
		want := "dashboard"
		switch {
		case width >= 23:
			want = "↓ more"
		case width >= 18:
			want = "↓"
		}
		if !strings.HasSuffix(header, want) {
			t.Errorf("width %d: the header is %q, want it to end in %q", width, header, want)
		}
	}
}

// The note is cut a word at a time: at twenty columns, where the terminal is
// most likely to be this small, it read "terminal too smal…".
func TestTheTooSmallNoteLosesWholeWordsBeforeItIsCut(t *testing.T) {
	for _, tc := range []struct {
		width int
		want  string
	}{
		{120, tooSmallNote}, {45, tooSmallNote}, {44, "terminal too small"}, {20, "terminal too small"},
		{19, "too small"}, {11, "too small"}, {10, "too sma…"}, {8, "too s…"},
	} {
		m, _ := realModel(t, tc.width, 6)
		if got := strings.TrimSpace(plain(m.tooSmallLine())); got != tc.want {
			t.Errorf("width %d: the note reads %q, want %q", tc.width, got, tc.want)
		}
	}
}
