package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func oneCall() []waitingCall {
	return []waitingCall{{agent: "claude", call: "note.rm 2", would: "would remove note 2: third note"}}
}

// learn is the model told what the queue held at its last look.
func learn(t *testing.T, m Model, calls []waitingCall) Model {
	t.Helper()
	next, _ := m.Update(waitingMsg{calls: calls})
	return next.(Model)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(plain(s), "\n")
	return line
}

// The reported miss: with a call parked the dashboard said so in a tile the
// sixth of a hundred and thirty rows down, and any other view said nothing.
func TestAParkedCallPutsOneLineAboveEveryScreenAndTakesItsRow(t *testing.T) {
	var done []int
	screens := map[string]func(*testing.T) Model{
		"dashboard": func(t *testing.T) Model { m, _ := realModel(t, 80, 24); return m },
		"result":    func(t *testing.T) Model { return listResult(t, listRegistry(t, &done)) },
		"plugins": func(t *testing.T) Model {
			m, _ := realModel(t, 80, 24)
			m.selected = 1
			return press(t, m, "p")
		},
	}
	for name, build := range screens {
		t.Run(name, func(t *testing.T) {
			m := build(t)
			rows := m.height
			if got := lipgloss.Height(m.View().Content); got > rows {
				t.Fatalf("a quiet screen is %d rows in a %d-row terminal", got, rows)
			}
			m = learn(t, m, oneCall())
			if m.banner != 1 || m.height != rows-1 {
				t.Fatalf("banner %d height %d, want one row taken from %d", m.banner, m.height, rows)
			}
			content := m.View().Content
			if line := firstLine(content); !strings.Contains(line, "1 call waiting") {
				t.Errorf("the first line is %q, not the waiting call", line)
			}
			if got := lipgloss.Height(content); got > rows {
				t.Errorf("with the line the screen is %d rows in a %d-row terminal", got, rows)
			}
			m = learn(t, m, nil)
			if m.banner != 0 || m.height != rows {
				t.Errorf("the line is gone and the row did not come back: banner %d height %d", m.banner, m.height)
			}
		})
	}
}

func TestTheLineNamesTheCallWhenThereIsOneAndCountsWhenThereAreMore(t *testing.T) {
	m, _ := realModel(t, 120, 30)
	one := firstLine(learn(t, m, oneCall()).View().Content)
	for _, want := range []string{"1 call waiting", "w to answer", "claude: note.rm 2", "would remove note 2: third note"} {
		if !strings.Contains(one, want) {
			t.Errorf("the line for one call lacks %q: %s", want, one)
		}
	}
	two := firstLine(learn(t, m, append(oneCall(), oneCall()...)).View().Content)
	if !strings.Contains(two, "2 calls waiting") || strings.Contains(two, "note.rm") {
		t.Errorf("the line for two calls should count them and name neither: %s", two)
	}
}

func TestTheLineIsOneRowWhateverTheCallSays(t *testing.T) {
	m, _ := realModel(t, 60, 24)
	long := waitingCall{agent: "claude", call: "kv.set " + strings.Repeat("x", 200), would: strings.Repeat("y", 300)}
	content := learn(t, m, []waitingCall{long}).View().Content
	if w := lipgloss.Width(firstLine(content)); w > 60 {
		t.Errorf("the line is %d cells wide in a 60-cell terminal", w)
	}
}

func TestATerminalTooShortToSpareARowGetsNoLine(t *testing.T) {
	m, _ := realModel(t, 80, 10)
	m = learn(t, m, oneCall())
	if m.banner != 0 || m.height != 10 {
		t.Errorf("banner %d height %d, want the screen left whole", m.banner, m.height)
	}
}

// A model that was sized by hand, as many are, keeps the size it was given.
func TestTheLineTakesItsRowFromTheHeightItWasGiven(t *testing.T) {
	m := dashboardModel(t)
	m.width, m.height = 100, 40
	m = learn(t, m, oneCall())
	if m.height != 39 {
		t.Errorf("height %d, want 39", m.height)
	}
	if m = learn(t, m, nil); m.height != 40 {
		t.Errorf("height %d after the call left, want 40", m.height)
	}
}

// What a click lands on moves with the row the line takes above the screen.
func TestAClickLandsOnTheTileUnderItWhileTheLineIsShown(t *testing.T) {
	m, _ := realModel(t, 120, 40)
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
	m = learn(t, m, oneCall())
	next, _ := m.Update(tea.MouseClickMsg{X: 5, Y: y + m.banner, Button: tea.MouseLeft})
	if got := next.(Model).selected; got != 1 {
		t.Errorf("a click on the first tile's cell selected %d", got)
	}
}
