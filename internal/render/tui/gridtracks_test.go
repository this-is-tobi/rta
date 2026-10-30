package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The grid is twelve tracks wide and a tile is some number of them. These hold
// what that has to be to read like the grid it replaced where nothing is stated,
// and like a web grid where something is.

// With nothing stated the dashboard is the columns it always was: two from 80
// cells, three from 120, four from 160, one below 80, and never past four.
func TestWithNoWidthStatedTheTerminalMakesTheColumns(t *testing.T) {
	for width, columns := range map[int]int{60: 1, 79: 1, 80: 2, 119: 2, 120: 3, 160: 4, 240: 4} {
		m := sectioned(t, config.Dashboard{Tiles: []config.Tile{
			{ID: "alpha.status"}, {ID: "beta.status"}, {ID: "gamma.status"}, {ID: "delta.status"}, {ID: "epsilon.status"},
		}}, width, 60)
		if got := len(m.tileRows()[0].cells); got != columns {
			t.Errorf("%d cells draw %d tiles to a row, want %d", width, got, columns)
		}
	}
}

// A row is exactly the screen wide however the tracks divide into cells, and
// tiles of one width differ by no more than a cell: the remainder falls at the
// right edge and not in the middle of the row.
func TestEveryRowIsExactlyTheScreenWideAtEveryWidth(t *testing.T) {
	for width := 40; width <= 250; width++ {
		m := sectioned(t, config.Dashboard{Width: 4, Tiles: []config.Tile{
			{ID: "alpha.status"}, {ID: "beta.status"}, {ID: "gamma.status", Width: 8},
			{ID: "delta.status", Width: 3}, {ID: "epsilon.status", Width: 9}, {ID: "zeta.status", Width: 12},
		}}, width, 60)
		for r, row := range m.tileRows() {
			total := len(row.cells) - 1
			for _, i := range row.cells {
				total += m.tileWidth(i)
			}
			if total != width {
				t.Fatalf("%d cells: row %d draws %d", width, r, total)
			}
		}
	}
}

// Tiles of one width differ by a cell at most.
func TestTilesOfOneWidthAreAsAlikeAsTheirCellsAllow(t *testing.T) {
	for width := 80; width <= 250; width += 7 {
		m := sectioned(t, config.Dashboard{Width: 3, Tiles: []config.Tile{
			{ID: "alpha.status"}, {ID: "beta.status"}, {ID: "gamma.status"}, {ID: "delta.status"},
		}}, width, 60)
		lo, hi := 1<<30, 0
		for _, i := range m.tileRows()[0].cells {
			w := m.tileWidth(i)
			lo, hi = min(lo, w), max(hi, w)
		}
		if hi-lo > 1 {
			t.Errorf("%d cells: four quarters are %d to %d wide", width, lo, hi)
		}
	}
}

// A width the tile's own entry states is a demand, and the screen's or a section's
// is a default that a capability's own minimum beats.
func TestADefaultWidthYieldsToACapabilitysMinimumAndADemandDoesNot(t *testing.T) {
	m := sectioned(t, config.Dashboard{
		Width:    3,
		Sections: map[string]config.DashboardSection{"wide": {Width: 6}},
		Tiles: []config.Tile{
			{ID: "alpha.status"},
			{ID: "beta.status", Section: "wide"},
			{ID: "gamma.status", Section: "wide", Width: 4},
		},
	}, 160, 60)
	for i, want := range map[int]int{1: 3, 2: 6, 3: 4} {
		if got := m.span(i); got != want {
			t.Errorf("tile %d is %d twelfths, want %d", i, got, want)
		}
	}
	m.tiles[1].cap.MinWidth = 100 // wants more than a quarter of 160 cells
	if got := m.span(1); got != 8 {
		t.Errorf("a quarter of 160 cells under a capability that needs 100 is %d twelfths, want the 8 that fit", got)
	}
	m.tiles[1].widthDemand = true // the same width, asked for by the tile itself
	if got := m.span(1); got != 3 {
		t.Errorf("a width the tile asked for is %d twelfths, want the 3 it said", got)
	}
}

// A width no terminal could draw is read as the least that it can, so one file
// holds on a small terminal: a quarter is not twenty cells.
func TestAWidthTooNarrowForTheTerminalIsTheLeastThatReads(t *testing.T) {
	m := sectioned(t, config.Dashboard{Width: 3, Tiles: []config.Tile{{ID: "alpha.status"}, {ID: "beta.status"}}}, 80, 60)
	if got := m.span(1); got != 6 {
		t.Errorf("a quarter on 80 cells is %d twelfths, want half", got)
	}
	for _, i := range []int{1, 2} {
		if w := m.tileWidth(i); w < minTileCells {
			t.Errorf("tile %d is %d cells wide, under the %d no tile is drawn at", i, w, minTileCells)
		}
	}
	// Narrower than a tile can be: one whole row.
	tiny := sectioned(t, config.Dashboard{Width: 3, Tiles: []config.Tile{{ID: "alpha.status"}, {ID: "beta.status"}}}, 30, 60)
	if got := tiny.span(1); got != config.GridTracks {
		t.Errorf("on 30 cells a tile is %d twelfths, want the whole row", got)
	}
}

// Halves, thirds and the rest are sentences of widths that add to twelve.
func TestWidthsThatAddToTwelveShareARow(t *testing.T) {
	m := sectioned(t, config.Dashboard{Tiles: []config.Tile{
		{ID: "alpha.status", Width: 4}, {ID: "beta.status", Width: 8},
		{ID: "gamma.status", Width: 3}, {ID: "delta.status", Width: 3}, {ID: "epsilon.status", Width: 6},
	}}, 160, 60)
	if got := shape(m); got != "[1 2] [3 4 5]" {
		t.Fatalf("rows = %s", got)
	}
	if a, b := m.tileWidth(1), m.tileWidth(2); a*2 > b || b > a*2+3 {
		t.Errorf("a third and two thirds are %d and %d cells", a, b)
	}
}

// A row that does not fill the grid shares the rest out, and the share is even.
func TestAShortRowSharesWhatIsLeftEvenly(t *testing.T) {
	m := sectioned(t, config.Dashboard{Tiles: []config.Tile{
		{ID: "alpha.status"}, {ID: "beta.status"}, {ID: "gamma.status"}, {ID: "delta.status"}, {ID: "epsilon.status"},
	}}, 132, 60) // three to a row: the second row holds two
	if got := shape(m); got != "[1 2 3] [4 5]" {
		t.Fatalf("rows = %s", got)
	}
	if a, b := m.tileWidth(4), m.tileWidth(5); a-b > 1 || b-a > 1 {
		t.Errorf("the two tiles of the short row are %d and %d cells: a leftover third went to one of them", a, b)
	}
}

// `height:` sets a row's height, and a row takes its tallest tile. A row that
// states none takes what its tiles need, as far as the screen has room.
func TestAStatedHeightLiftsTheCapAndTheRowTakesTheTallest(t *testing.T) {
	build := func(tall, other int) Model {
		m := New(heightRegistry(t), config.Dashboard{Width: 6, Tiles: []config.Tile{
			{ID: "tall.info", Height: tall}, {ID: "short.info", Height: other},
		}}, nil)
		sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
		sm := sized.(Model)
		sm.tiles[1].view = view.Text{Body: strings.Repeat("line\n", 40)}
		sm.tiles[2].view = view.Text{Body: "short"}
		return sm
	}
	if got, want := build(0, 0).rowHeights()[0], 40+2; got != want {
		t.Fatalf("with none stated a row is %d lines, want the %d its forty lines need", got, want)
	}
	if got := build(20, 0).rowHeights()[0]; got != 20 {
		t.Errorf("a tile that says 20 draws its row at %d", got)
	}
	if got := build(0, 20).rowHeights()[0]; got != 20 {
		t.Errorf("a short tile that says 20 does not set its row: %d", got)
	}
	if got := build(8, 14).rowHeights()[0]; got != 14 {
		t.Errorf("the taller of two stated heights is %d, want 14", got)
	}
	// A small one is as small as it says, where the floor is otherwise six.
	small := New(heightRegistry(t), config.Dashboard{Width: 6, Tiles: []config.Tile{
		{ID: "tall.info", Height: config.MinHeight}, {ID: "short.info", Height: config.MinHeight},
	}}, nil)
	sized, _ := small.Update(tea.WindowSizeMsg{Width: 100, Height: 60})
	if got := sized.(Model).rowHeights()[0]; got != config.MinHeight {
		t.Errorf("two tiles that say %d draw their row at %d", config.MinHeight, got)
	}
}

// A height a section or the screen states is the default of the tiles under it,
// and the nearest wins, as a width does.
func TestHeightFollowsTheSameCascadeAsWidth(t *testing.T) {
	m := sectioned(t, config.Dashboard{
		Height:   10,
		Sections: map[string]config.DashboardSection{"tall": {Height: 16}},
		Tiles: []config.Tile{
			{ID: "alpha.status"},
			{ID: "beta.status", Section: "tall"},
			{ID: "gamma.status", Section: "tall", Height: 8},
		},
	}, 100, 60)
	for i, want := range map[int]int{1: 10, 2: 16, 3: 8} {
		if got := m.tiles[i].height; got != want {
			t.Errorf("tile %d is %d lines, want %d", i, got, want)
		}
	}
}

// A stated height is held to the screen it is drawn on: a row never outgrows it.
func TestAStatedHeightNeverOutgrowsTheScreen(t *testing.T) {
	m := sectioned(t, config.Dashboard{Tiles: []config.Tile{{ID: "alpha.status", Height: config.MaxHeight}}}, 100, 24)
	if got, room := m.rowHeights()[0], m.dashRowBudget(); got > room {
		t.Errorf("a row of %d lines on a screen with room for %d", got, room)
	}
}

// What a person typed is what is drawn: the tile's box is its stated number of lines.
func TestAStatedHeightIsTheNumberOfLinesTheBoxIsDrawnAt(t *testing.T) {
	m := sectioned(t, config.Dashboard{Width: 6, Tiles: []config.Tile{{ID: "alpha.status", Height: 12}}}, 100, 60)
	m.tiles[1].view = view.Text{Body: "one"}
	frame := m.View().Content
	lines := 0
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, "│") || strings.Contains(l, "╭") || strings.Contains(l, "╰") {
			lines++
		}
	}
	if lines < 12 {
		t.Errorf("a tile that says 12 lines is drawn in %d box lines:\n%s", lines, frame)
	}
}
