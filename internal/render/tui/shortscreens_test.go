package tui

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// shortSizes are the terminals the audit found the overlay and the dialog broken
// on: a laptop split, a status-bar-sized pane, and the classic width with too few
// rows.
var shortSizes = [][2]int{{44, 14}, {60, 14}, {80, 12}, {80, 14}}

// missingFromTheOverlay is the keys the screen under the overlay answers, with the
// label each carries, that are not on it: every one has to be, or the overlay is a
// list of some of them.
func missingFromTheOverlay(m Model) []string {
	out := ansi.Strip(m.helpView())
	var missing []string
	for _, it := range m.footerItems(m.mode) {
		row := regexp.MustCompile(regexp.QuoteMeta(it.display) + `\s+` + regexp.QuoteMeta(it.label))
		if !row.MatchString(out) {
			missing = append(missing, it.display+" "+it.label)
		}
	}
	return missing
}

// The overlay clamped its panel to the screen and could not scroll, so on a short
// terminal it listed the first dozen keys and cut the rest: at 44x14 the real
// note.list lost esc, q and ?, the keys that say how to leave. A list taller than
// the screen takes two columns.
func TestTheKeyOverlayHoldsEveryKeyOnAShortTerminal(t *testing.T) {
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	screens := map[string]func(w, h int) Model{
		"the note.list result": func(w, h int) Model {
			add := mustCap(t, realRegistry(t), "note.add")
			if _, err := add.Run(context.Background(), plugin.NewRequest(map[string]any{"title": "first"}, false, false)); err != nil {
				t.Fatal(err)
			}
			return sent(t, screenOf(t, "note.list", nil), tea.WindowSizeMsg{Width: w, Height: h})
		},
		"the dashboard on a tile": func(w, h int) Model {
			m, _ := realModel(t, w, h)
			m.selected = 1
			return m
		},
	}
	for name, build := range screens {
		for _, size := range shortSizes {
			m := build(size[0], size[1])
			m.help = true
			label := fmt.Sprintf("%s at %dx%d", name, size[0], size[1])
			if missing := missingFromTheOverlay(m); len(missing) > 0 {
				t.Errorf("%s: the overlay does not list %v:\n%s", label, missing, ansi.Strip(m.helpView()))
			}
			frame := strings.Split(ansi.Strip(m.View().Content), "\n")
			if len(frame) > size[1] {
				t.Errorf("%s: the frame is %d lines", label, len(frame))
			}
			for i, line := range frame {
				if w := ansi.StringWidth(line); w > size[0] {
					t.Errorf("%s: line %d is %d cells wide", label, i, w)
				}
			}
		}
	}
}

// A terminal with the room keeps the one column it always had.
func TestTheKeyOverlayKeepsOneColumnWhereThereIsRoom(t *testing.T) {
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	m := sent(t, screenOf(t, "note.list", nil), tea.WindowSizeMsg{Width: 100, Height: 40})
	m.help = true
	if missing := missingFromTheOverlay(m); len(missing) > 0 {
		t.Fatalf("the overlay does not list %v", missing)
	}
	for _, line := range strings.Split(ansi.Strip(m.helpView()), "\n") {
		if strings.Count(line, "│") > 2 {
			t.Errorf("a column break where one column fits: %q", line)
		}
	}
}

// The dialog was centred on the whole screen, and its bar is the last lines of
// it: on a short terminal the box sat on the keys that answer it, and only the
// first word of the bar showed to the left of the box. The bar goes to the bottom
// of the screen and the box is centred on what is above it.
func TestTheDialogNeverCoversItsOwnKeyBar(t *testing.T) {
	for _, size := range append(shortSizes, [2]int{80, 16}, [2]int{100, 30}, [2]int{120, 40}) {
		m := sent(t, questionOpen(t), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		label := fmt.Sprintf("%dx%d", size[0], size[1])
		g := m.dialogGeometry()
		bar := strings.Split(ansi.Strip(m.footerFor(m.mode)), "\n")
		frame := strings.Split(ansi.Strip(m.View().Content), "\n")
		if len(frame) != size[1] {
			t.Errorf("%s: the frame is %d lines, want the screen's %d", label, len(frame), size[1])
			continue
		}
		if end := g.y + len(g.lines); end > size[1]-len(bar) {
			t.Errorf("%s: the box ends on row %d and the bar starts on row %d", label, end, size[1]-len(bar))
		}
		for i, want := range bar {
			if got := strings.TrimRight(frame[size[1]-len(bar)+i], " "); got != strings.TrimRight(want, " ") {
				t.Errorf("%s: the bar's line %d reads %q on the screen, want %q", label, i, got, want)
			}
		}
		if g.buttonRow >= size[1]-len(bar) {
			t.Errorf("%s: the buttons are on row %d, inside the bar", label, g.buttonRow)
		}
		for _, want := range []string{"Lock the store?", "Lock store", "Cancel"} {
			if !strings.Contains(strings.Join(frame, "\n"), want) {
				t.Errorf("%s: the frame lacks %q", label, want)
			}
		}
	}
}
