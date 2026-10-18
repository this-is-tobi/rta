package tui

import (
	"fmt"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// scrollTiles is a dashboard whose rows are not all one height: tall rows first,
// short ones after, so that the rows which fit from the top are fewer than the
// rows which fit from further down.
func scrollTiles(ids ...string) config.Dashboard {
	var tiles []config.Tile
	for _, id := range ids {
		tiles = append(tiles, config.Tile{ID: id})
	}
	return config.Dashboard{Tiles: tiles}
}

// selectionIsDrawn is whether the row the selection is on is one of the rows the
// frame draws, which is what the selected tile's border, the footer's keys and
// enter all take for granted.
func selectionIsDrawn(m Model) bool {
	row, _ := m.selectedRow()
	first, last := m.rowWindow(len(m.tileRows()))
	return row >= first && row < last
}

// clampScroll raised the offset to put the selection on the window's last row,
// counting the rows that fit from the offset it started at. Rows are not all one
// height, and from further down more of them fit, so the offset it arrived at
// could pass maxScroll; rowWindow then started the window at maxScroll and
// counted from the offset, one row short, and the last row was not drawn.
func TestEndSelectsATileTheWindowDraws(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	for name, dash := range map[string]config.Dashboard{
		"two tall rows then short ones": scrollTiles("tall.info", "tall.info", "short.info", "short.info", "short.info"),
		"two mid rows then short ones":  scrollTiles("mid.info", "mid.info", "short.info", "short.info", "short.info"),
		"tall, mid, then four short":    scrollTiles("tall.info", "mid.info", "short.info", "short.info", "short.info", "short.info"),
	} {
		for h := 12; h <= 40; h++ {
			m := filled(t, New(heightRegistry(t), dash, nil), 60, h)
			m.selectEdge(true)
			if first, last := m.rowWindow(len(m.tileRows())); !selectionIsDrawn(m) || m.scroll > m.maxScroll() {
				row, _ := m.selectedRow()
				t.Errorf("%s at 60x%d: end selects row %d, the window is [%d, %d), scroll %d, maxScroll %d, heights %v",
					name, h, row, first, last, m.scroll, m.maxScroll(), m.rowHeights())
				break
			}
		}
	}
}

// The case the sweep above found at 60x24: two rows of ten lines, three of six.
// From the top two rows fit, so the offset went to row 3; from row 2 all three
// of the short ones fit, which is why maxScroll is 2.
func TestTheLastRowIsDrawnWhereMoreRowsFitFromFurtherDown(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := filled(t, New(heightRegistry(t), scrollTiles("tall.info", "tall.info", "short.info", "short.info", "short.info"), nil), 60, 24)
	if got := m.rowHeights(); fmt.Sprint(got) != "[10 10 6 6 6]" || m.maxScroll() != 2 {
		t.Fatalf("the fixture drifted: heights %v, maxScroll %d, want [10 10 6 6 6] and 2", got, m.maxScroll())
	}
	m.selectEdge(true)
	if first, last := m.rowWindow(5); m.scroll != 2 || first != 2 || last != 5 {
		t.Errorf("end: scroll %d, window [%d, %d), want 2 and [2, 5)", m.scroll, first, last)
	}
}

// Shrinking the window is the other way to get there: the selection sits on the
// last tile of a terminal that held everything, and the smaller one has to bring
// it into view, in rows of the height they have at the new width.
func TestShrinkingTheTerminalKeepsTheSelectionDrawn(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	// At 160 columns the six tiles share a few rows and all of them are drawn; at
	// sixty cells they are six rows of three heights.
	dash := scrollTiles("tall.info", "mid.info", "short.info", "short.info", "short.info", "short.info")
	m := filled(t, New(heightRegistry(t), dash, nil), 160, 50)
	m.selectEdge(true)
	if !selectionIsDrawn(m) {
		t.Fatal("the selection is not drawn at 160x50, before anything was shrunk")
	}
	for _, size := range []struct{ w, h int }{{80, 24}, {60, 24}, {60, 30}, {60, 36}, {44, 14}} {
		next, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		small := next.(Model)
		if !selectionIsDrawn(small) {
			first, last := small.rowWindow(len(small.tileRows()))
			row, _ := small.selectedRow()
			t.Errorf("160x50 shrunk to %dx%d: the selection is on row %d and the window is [%d, %d)", size.w, size.h, row, first, last)
		}
	}
}

// The wheel moves the window and not the selection, so a selection the window
// left stayed where nobody could see it, with enter, H and [ ] still acting on
// it. The catalogue's wheel moves its cursor; the dashboard's now brings the
// selection to the nearest row it draws.
func TestTheWheelBringsTheSelectionToTheNearestDrawnRow(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := filled(t, New(heightRegistry(t), scrollTiles("tall.info", "tall.info", "short.info", "short.info", "short.info"), nil), 60, 24)
	wheel := func(m Model, button tea.MouseButton, notches int) Model {
		for range notches {
			m = sent(t, m, tea.MouseWheelMsg{Button: button})
		}
		return m
	}
	rowOf := func(m Model) int { row, _ := m.selectedRow(); return row }

	m.selected = 1
	m.clampScroll()
	if first, last := m.rowWindow(5); first != 0 || last != 2 || rowOf(m) != 0 {
		t.Fatalf("the fixture drifted: window [%d, %d), selection on row %d, want [0, 2) and 0", first, last, rowOf(m))
	}

	// Down to the end: the window is [2, 5) and the selection was on row 0.
	down := wheel(m, tea.MouseWheelDown, 2)
	if first, last := down.rowWindow(5); first != 2 || last != 5 {
		t.Fatalf("two notches down leave the window at [%d, %d), want [2, 5)", first, last)
	}
	if row := rowOf(down); row != 2 {
		t.Errorf("the selection is on row %d, want 2, the first row the window draws", row)
	}

	// A selection the window still holds is left where it is: row 1 stays in
	// [1, 3) after one notch.
	held := m
	held.selected = 2
	if one := wheel(held, tea.MouseWheelDown, 1); one.selected != held.selected {
		t.Errorf("one notch moved the selection from tile %d to %d, though row 1 is still drawn", held.selected, one.selected)
	}

	// Up from the end with the selection on the last row: the window is [0, 2) again.
	m.selectEdge(true)
	if row := rowOf(m); row != 4 {
		t.Fatalf("end selected row %d, want 4", row)
	}
	up := wheel(m, tea.MouseWheelUp, 2)
	if first, last := up.rowWindow(5); first != 0 || last != 2 {
		t.Fatalf("two notches up leave the window at [%d, %d), want [0, 2)", first, last)
	}
	if row := rowOf(up); row != 1 {
		t.Errorf("the selection is on row %d, want 1, the last row the window draws", row)
	}

	// The search bar is not a row: it never scrolls, so nothing leaves it behind.
	bar := m
	bar.selected = 0
	if got := wheel(bar, tea.MouseWheelDown, 2); got.selected != 0 {
		t.Errorf("the wheel moved the selection off the search bar to tile %d", got.selected)
	}
}

// Sections add a header row that can be the selection, and rows of a different
// height than the tiles under them. Whatever the wheel does from wherever the
// selection is, what it selects is on the screen.
func TestTheWheelNeverLeavesTheSelectionOffScreen(t *testing.T) {
	var tiles []config.Tile
	for i := range 8 {
		tiles = append(tiles, config.Tile{ID: "alpha.status", Profile: fmt.Sprintf("p%d", i), Section: fmt.Sprintf("s%d", i/2)})
	}
	base := sectioned(t, config.Dashboard{Tiles: tiles}, 40, 16)
	starts := map[string]func(Model) Model{
		"first tile": func(m Model) Model { m.selected = 1; return m },
		"last tile":  func(m Model) Model { m.selectEdge(true); return m },
		"a header":   func(m Model) Model { m.selectStop(stop{header: "s1"}); return m },
	}
	for name, start := range starts {
		for _, button := range []tea.MouseButton{tea.MouseWheelDown, tea.MouseWheelUp} {
			m := start(base)
			m.clampScroll()
			for notch := 1; notch <= len(m.tileRows()); notch++ {
				m = sent(t, m, tea.MouseWheelMsg{Button: button})
				if !selectionIsDrawn(m) {
					first, last := m.rowWindow(len(m.tileRows()))
					row, _ := m.selectedRow()
					t.Errorf("from %s, notch %d of %v: the selection is on row %d and the window is [%d, %d)", name, notch, button, row, first, last)
					break
				}
			}
		}
	}
}
