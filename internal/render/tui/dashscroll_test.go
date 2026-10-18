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
