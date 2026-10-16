package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Laying the dashboard out and painting it: how tiles become rows and
// columns at a width, which rows fit, and what one tile renders as —
// including tile zero, the search bar.

// The dashboard's grid is config.GridTracks wide and a tile is some number of
// tracks: its width, in twelfths of the screen. Everything below that decides a
// tile's place is arithmetic on the terminal's width and those numbers, in one
// spot, so that painting, hit-testing, scrolling and moving the selection read the
// same layout.

// minColWidth is the narrowest a tile gets before its summaries stop being
// readable. It is derived from where the dashboard already stood: two columns from
// 80 cells up, which is 39 cells and a gap.
const minColWidth = 40

// minTileCells is the least a tile is drawn at, in cells: a width that would give
// less is read as the least that gives this, which is how one file holds on a
// terminal of any size. At 80 cells that is half the screen, at 120 a third.
const minTileCells = minColWidth - 1

// maxCols caps the automatic grid. Past four the tiles are narrow enough that
// the eye stops scanning columns and starts hunting, and a dashboard you hunt
// through is a dashboard you stop opening.
const maxCols = 4

// edge is the cell track j starts at: the screen's width and the gap cell after
// each tile shared out over the tracks, so that a tile's width is the distance
// between its two edges less its gap, tiles of equal width differ by at most a
// cell, and the last one ends exactly at the right edge. Rounded down, so the
// remainder falls on the right of the row and not in the middle of it.
func (m Model) edge(track int) int {
	return track * (m.width + 1) / config.GridTracks
}

// cellsOf is how wide, in cells, the narrowest tile of span tracks is drawn.
func (m Model) cellsOf(span int) int { return m.edge(span) - 1 }

// tracksFor is the least number of tracks that draws a tile at least cells wide:
// a capability's MinWidth, or the floor below which nothing is readable.
func (m Model) tracksFor(cells int) int {
	for span := 1; span < config.GridTracks; span++ {
		if m.cellsOf(span) >= cells {
			return span
		}
	}
	return config.GridTracks
}

// minTracks is the least width any tile has: the terminal's own floor, one whole
// row when the terminal is narrower than a tile can be.
func (m Model) minTracks() int {
	if m.width < minColWidth {
		return config.GridTracks
	}
	return m.tracksFor(minTileCells)
}

// autoSpan is the width a tile gets when nothing states one: as many columns as
// the terminal has room for, to a most of four, each a whole number of tracks
// (twelve divides by 1, 2, 3 and 4), so the dashboard of a screen that states no
// width is the dashboard it always was.
//
// The dashboard was two columns at every width, so a 200-cell terminal drew two
// 100-cell tiles of a six-line summary — half the screen spent on whitespace,
// with the tiles that did not fit pushed below the fold.
func (m Model) autoSpan() int {
	return config.GridTracks / max(min(m.width/minColWidth, maxCols), 1)
}

// span is how many tracks tile i is wide at the current width of the terminal.
//
// A width the tile's own entry states is a demand and is kept, but for the floor
// every tile has. One a section or the screen states is a default, and it yields
// to the capability's own minimum (MinWidth) exactly as a pace yields to a
// declared one: the default was never about the tile that needs more room. With
// none stated the tile has the width the terminal makes of it.
func (m Model) span(i int) int {
	if i <= 0 || i >= len(m.tiles) {
		return 1
	}
	t := m.tiles[i]
	floor := m.minTracks()
	if t.width > 0 && t.widthDemand {
		return min(max(t.width, floor), config.GridTracks)
	}
	s := m.autoSpan()
	if t.width > 0 {
		s = t.width
	}
	s = max(s, m.tracksFor(t.cap.MinWidth), floor)
	return min(s, config.GridTracks)
}

// tileRows groups the capability tiles into the rows they are drawn on: a
// wide tile takes a row to itself, the rest fill the grid. A section is a
// header row and, unless it is folded, the rows of its tiles on the grid it
// states; the tiles in no section are laid out on the dashboard's, and a header
// ends the row before it, so a section never shares a line with a stranger.
//
// This is the one place that decides which tile sits on which row. It used
// to be `i/cols` arithmetic repeated across rendering, hit-testing, scroll
// paging and selection movement — four copies of one rule, correct only for
// as long as every row held exactly cols tiles, and all four of which had to
// be found and changed together the moment one did not.
func (m Model) tileRows() []gridRow {
	runs := m.sectionRuns()
	var rows []gridRow
	next := 0
	for i := 1; i < len(m.tiles); {
		if next < len(runs) && runs[next].lo == i {
			run := runs[next]
			next++
			rows = append(rows, gridRow{header: run})
			if !m.collapsed(run.name) {
				rows = append(rows, m.packRows(run.lo, run.hi)...)
			}
			i = run.hi
			continue
		}
		end := len(m.tiles)
		if next < len(runs) {
			end = runs[next].lo
		}
		rows = append(rows, m.packRows(i, end)...)
		i = end
	}
	return rows
}

// tileWidth is how wide one tile is drawn, in cells: its span in tracks, plus
// whatever a short row has left over.
//
// The leftover matters. A wide tile forces the row before it to close early,
// so with nine tiles in two columns the eighth used to be drawn at half
// width with dead space beside it and nothing to explain the hole. A row that
// does not fill the grid shares out what is left, so a short row reads as a
// deliberate arrangement instead of a rendering accident; what does not divide
// goes to the last tile, so a row is always exactly the screen wide and its right
// edge lines up with every other row's.
func (m Model) tileWidth(i int) int {
	cells := m.rowOf(i)
	if len(cells) == 0 {
		return max(m.cellsOf(m.autoSpan()), 1)
	}
	spans := make([]int, len(cells))
	taken := 0
	for k, idx := range cells {
		spans[k] = m.span(idx)
		taken += spans[k]
	}
	if left := config.GridTracks - taken; left > 0 {
		for k := range spans {
			spans[k] += left / len(cells)
		}
		spans[len(spans)-1] += left % len(cells)
	}
	start := 0
	for k, idx := range cells {
		if idx == i {
			return max(m.edge(start+spans[k])-m.edge(start)-1, 1)
		}
		start += spans[k]
	}
	return max(m.cellsOf(m.autoSpan()), 1)
}

// rowOf returns the row tile i sits on.
func (m Model) rowOf(i int) []int {
	for _, row := range m.tileRows() {
		if slices.Contains(row.cells, i) {
			return row.cells
		}
	}
	return nil
}

// rowAndCol locates a tile in the row layout: the index of its row among all
// the rows, headers included, and its column in it. -1 for a tile that is not on
// a row, which is one in a section that is folded.
func (m Model) rowAndCol(idx int) (int, int) {
	for r, row := range m.tileRows() {
		for c, i := range row.cells {
			if i == idx {
				return r, c
			}
		}
	}
	return -1, -1
}

// rowHeights returns each tile row's height, in row order: the tallest
// natural content among that row's tiles, clamped to [tileMinHeight,
// tileHeight]. Tiles within a row always share the row's height, the same
// invariant TestTilesOnSameRowAreUniform checks — only the height itself now
// varies row to row instead of being one constant for the whole grid. A header
// is the one row that is one line, whatever is under it.
func (m Model) rowHeights() []int {
	rows := m.tileRows()
	heights := make([]int, 0, len(rows))
	limit := m.rowLimit()
	for _, row := range rows {
		if row.isHeader() {
			heights = append(heights, sectionHeaderHeight)
			continue
		}
		heights = append(heights, m.rowHeight(row, limit))
	}
	return heights
}

// rowHeight is how tall one row of tiles is drawn: the tallest of what its tiles
// need, where a tile that states a height needs exactly that and one that does
// not needs what it has to say (tileRowHeight), held between the least a panel
// reads as one and limit.
//
// A stated height is not held to limit, since the limit is the cap on guessing
// and a person who wrote `height: 20` has said how much room the tile gets; it is
// held to the room there is on the screen, so a row never outgrows it. Tiles on a
// row always share its height, so an explicit one sets the row for the tiles
// beside it.
func (m Model) rowHeight(row gridRow, limit int) int {
	stated, natural := 0, 0
	for _, i := range row.cells {
		if h := m.tiles[i].height; h > 0 {
			stated = max(stated, h)
			continue
		}
		natural = max(natural, tileRowHeight(m.asReturned(i), m.tileWidth(i)))
	}
	if stated > 0 && m.height > 0 {
		stated = min(stated, max(m.dashRowBudget(), tileClipHeight))
	}
	if natural == 0 {
		return stated
	}
	return max(stated, min(max(natural, tileMinHeight), limit))
}

// rowLimit is the tallest a row is drawn: tileHeight, unless the room under
// the search bar holds less than two of them, in which case half of it. A row
// has always been allowed up to tileHeight lines, so on a terminal 24 lines
// high the first row took eleven of the twenty that were left and the second
// did not fit at all; two rows clipped at "… enter for details" show twice the
// tiles of one drawn whole, and the clipped line is where enter goes for the
// rest. Never under tileMinHeight, which is the least a panel reads as one.
func (m Model) rowLimit() int {
	if m.height <= 0 {
		return tileHeight
	}
	return min(tileHeight, max(tileMinHeight, m.dashRowBudget()/2))
}

// visibleRowCount is how many rows starting at from fit within avail lines,
// given each row's own height — the shared arithmetic tileAt, dashRowsVisible
// and dashboardView all need to agree on, now that a row's height is no
// longer one constant they could each compute independently.
//
// One row is the floor even when that row does not fit: a dashboard that
// draws nothing is worse than one whose tallest row is clipped. The clipping
// is drawnHeights' job — handed to the renderer at its full height, that row
// overflowed the terminal and the footer was the part that fell off.
func visibleRowCount(heights []int, from, avail int) int {
	count, used := 0, 0
	for i := from; i < len(heights); i++ {
		used += heights[i]
		if used > avail {
			break
		}
		count++
	}
	return max(1, count)
}

// tileAt maps a terminal cell to a tile index, -1 for none. Row 0 is the
// header, then the full-width search bar, then tile rows — each its own
// height — offset by the scroll position. A section's header is none: it is a
// row, and headerAt is what finds it.
func (m Model) tileAt(x, y int) int {
	if y < 1 {
		return -1
	}
	if y < 1+m.searchHeight() {
		return 0
	}
	row, ok := m.gridRowAt(y)
	if !ok || row.isHeader() {
		return -1
	}
	// Walk the row by drawn width. Dividing x by one column width only ever
	// worked while every tile was one column wide, and the special case for
	// a single full-width tile was that assumption's one exception rather
	// than a rule — a two-column tile beside a one-column one landed every
	// click in the row on the wrong tile.
	for _, i := range row.cells {
		w := m.tileWidth(i)
		if x < w {
			return i
		}
		x -= w + 1 // the gap cell between tiles belongs to neither
		if x < 0 {
			return -1
		}
	}
	return -1
}

// gridRowAt is the row the terminal line y is drawn on, and whether it is on
// one: the header, the search bar and the footer area below the last row are
// not. The rows are walked at the heights they were drawn at (drawnHeights), so
// a click lands where the paint did.
func (m Model) gridRowAt(y int) (gridRow, bool) {
	rows := m.tileRows()
	if len(rows) == 0 || !m.tilesFit() {
		return gridRow{}, false
	}
	first, last := m.rowWindow(len(rows))
	yy := y - 1 - m.searchHeight()
	if yy < 0 {
		return gridRow{}, false
	}
	for r, h := range m.drawnHeights(first, last) {
		if yy < h {
			return rows[first+r], true
		}
		yy -= h
	}
	return gridRow{}, false
}

// dashRowsVisible reports how many tile rows fit under header + search bar,
// starting from the current scroll position.
func (m Model) dashRowsVisible() int {
	if m.height <= 0 {
		// Size unknown. Model.View paints nothing in that state, but this is
		// still reached: clampScroll asks the same question, and a keypress
		// can land before the first WindowSizeMsg does, since both arrive
		// from goroutines. Everything rather than an arbitrary window, which
		// makes clampScroll's min a no-op instead of a guess.
		return 1 << 30
	}
	return visibleRowCount(m.rowHeights(), m.scroll, m.dashRowBudget())
}

// dashRowBudget is how many lines the tile rows have between the header with
// the search bar under it and the footer.
func (m Model) dashRowBudget() int {
	return m.height - 1 - lipgloss.Height(m.dashFooter()) - m.searchHeight()
}

// tilesFit is whether the room under the search holds the least a tile row is
// drawn at (tileClipHeight). When it does not, no row is drawn: the one row
// visibleRowCount admits without the room for it was cut by the screen's own
// edge, which left a box with its top border and the line that says there is
// more, and nothing a person could read — and a click on it opened a tile that
// was not there. An unknown size fits everything, as dashRowsVisible says.
func (m Model) tilesFit() bool {
	return m.height <= 0 || m.dashRowBudget() >= tileClipHeight
}

// rowWindow is the half-open range of tile rows on screen: from the scroll
// offset, as many as dashRowsVisible admits.
func (m Model) rowWindow(rows int) (first, last int) {
	// Capped by where the window can start, because the room is not constant:
	// closing the search hands back four lines, and an offset that was right
	// with the box open would leave the last row alone above dead space until
	// the next tile answered and clampScroll caught up.
	first = min(m.scroll, rows-1, m.maxScroll())
	return first, min(first+m.dashRowsVisible(), rows)
}

// drawnHeights is what the rows in [first, last) are drawn at, and what tileAt
// walks, so a click lands where the paint did. Each row's own height, except
// the one row visibleRowCount admits without the room for it, which is drawn
// at the room there is — never less than its borders and the line that says
// there is more.
func (m Model) drawnHeights(first, last int) []int {
	heights := m.rowHeights()[first:last]
	if budget := m.dashRowBudget(); m.height > 0 && len(heights) == 1 && heights[0] > budget {
		heights[0] = max(budget, tileClipHeight)
	}
	return heights
}

// dashRows is the total number of capability-tile rows at the current width.
func (m Model) dashRows() int { return len(m.tileRows()) }

// maxScroll is the furthest the window may start: the first row from which
// everything to the end fits, so that scrolling to the end fills the screen
// instead of leaving the last row alone above dead space. Rows have their own
// heights, so this is a walk back from the end rather than dashRows minus a
// constant — the two agree while every row is the same height, and only the
// walk stays right when a tile's answer changes its row. When not even the
// last row fits by itself, the last row is where the end is.
func (m Model) maxScroll() int {
	if m.height <= 0 {
		return 0 // size unknown: dashRowsVisible admits everything from the top
	}
	heights := m.rowHeights()
	budget, used := m.dashRowBudget(), 0
	for s := len(heights) - 1; s >= 0; s-- {
		used += heights[s]
		if used > budget {
			return min(s+1, len(heights)-1)
		}
	}
	return 0
}

// clampScroll keeps the selected tile visible and the offset in bounds. The
// search bar never scrolls; capability tiles window selection-driven.
//
// A tile that scrolls into view from above brings its section's header with it
// when it is the first row under it: a window that opened on the middle of a
// section says nothing about which section that is.
func (m *Model) clampScroll() {
	m.scroll = min(m.scroll, m.maxScroll())
	if m.selected == 0 {
		return
	}
	selRow, _ := m.selectedRow()
	if selRow < 0 {
		return
	}
	if selRow < m.scroll {
		m.scroll = selRow
		if rows := m.tileRows(); selRow > 0 && rows[selRow-1].isHeader() {
			m.scroll = selRow - 1
		}
	}
	// Asked after the window has moved: how many rows fit depends on which rows
	// they are, and the header that came along may leave room for one fewer.
	if visible := m.dashRowsVisible(); selRow >= m.scroll+visible {
		m.scroll = selRow - visible + 1
	}
}

// selectedRow is the row the selection is on and the column in it: a tile's, or
// the header row of the section that holds the selection, whose column is the
// one the reader last aimed for. -1 when the selection is on none, which is the
// search bar, or a tile in a section that is folded.
func (m Model) selectedRow() (row, col int) {
	if m.selected >= 0 {
		return m.rowAndCol(m.selected)
	}
	for r, gr := range m.tileRows() {
		if gr.isHeader() && gr.header.name == m.header {
			return r, m.goalCol
		}
	}
	return -1, -1
}

// stop is one place the selection rests: a tile, or the header of a section.
type stop struct {
	tile   int
	header string
	col    int
}

// stops are the places the selection can rest, in the order they are read: each
// header and the tiles under it, row by row. The members of a folded section are
// not there, since they are on no row, so the arrows pass through the header of
// one and skip what it holds.
func (m Model) stops() []stop {
	out := make([]stop, 0, len(m.tiles))
	for _, row := range m.tileRows() {
		if row.isHeader() {
			out = append(out, stop{header: row.header.name})
			continue
		}
		for c, i := range row.cells {
			out = append(out, stop{tile: i, col: c})
		}
	}
	return out
}

// currentStop is the stop that holds the selection, whether or not the layout
// has it on a row.
func (m Model) currentStop() stop {
	if m.selected < 0 {
		return stop{header: m.header}
	}
	return stop{tile: m.selected}
}

// selectStop puts the selection on s. A header is selected as -1 with the section
// named beside it, so that everything that reads a tile's index off the selection
// finds none rather than a tile that is not the one in front of the reader.
func (m *Model) selectStop(s stop) {
	if s.header == "" {
		m.selected, m.header = s.tile, ""
		return
	}
	m.selected, m.header = -1, s.header
}

// moveSelection shifts the selection by (dx, dy): the search bar sits above the
// grid, tiles and the headers of sections move on it.
func (m *Model) moveSelection(dx, dy int) {
	defer m.clampScroll()
	if m.selected == 0 {
		if dy > 0 || dx > 0 {
			if stops := m.stops(); len(stops) > 0 {
				m.selectStop(stops[0])
			}
		}
		m.goalCol = 0
		return
	}
	// Left and right walk the stops in order, which is what they look like
	// they do. Up and down move between rows and keep the column, which is
	// only the same thing while every row is the same length — and a
	// full-width tile makes one of them shorter.
	if dx != 0 {
		stops, here := m.stops(), m.currentStop()
		at := slices.IndexFunc(stops, func(s stop) bool { return s.tile == here.tile && s.header == here.header })
		if at < 0 {
			return
		}
		next := stops[min(max(at+dx, 0), len(stops)-1)]
		m.selectStop(next)
		// Moving sideways is choosing a column, so it sets the one vertical
		// movement will aim for.
		m.goalCol = next.col
		return
	}
	if dy == 0 {
		return
	}
	rows := m.tileRows()
	row, col := m.selectedRow()
	if row < 0 {
		return
	}
	// Aim for the column the reader last chose, not the one they happen to
	// be in. Rows are not all the same length — a wide tile closes the row
	// before it early — so reading the column back off the current position
	// let a short row swallow it: from the right-hand column, four presses
	// down and four back up landed on the left-hand column, having passed
	// through a one-tile row on the way. Every text editor solves this the
	// same way and for the same reason.
	if m.goalCol > col {
		col = m.goalCol
	}
	target := row + dy
	if target < 0 {
		m.selected, m.header = 0, "" // up from the top row lands on search
		return
	}
	if target >= len(rows) {
		return
	}
	m.goalCol = col
	if rows[target].isHeader() {
		m.selectStop(stop{header: rows[target].header.name})
		return
	}
	m.selectStop(stop{tile: rows[target].cells[min(col, len(rows[target].cells)-1)]})
}

// moveBack is shift+tab, the way back along what tab walks: one stop earlier, and
// from the first of them the search bar, which is where tab left from. Tab was
// advertised as a way to select and had no return, so the only way back to the
// bar from the last tile of a long dashboard was one arrow at a time.
func (m *Model) moveBack() {
	defer m.clampScroll()
	if m.selected == 0 {
		return
	}
	stops, here := m.stops(), m.currentStop()
	at := slices.IndexFunc(stops, func(s stop) bool { return s.tile == here.tile && s.header == here.header })
	if at > 0 {
		m.moveSelection(-1, 0)
		return
	}
	m.selected, m.header, m.goalCol = 0, "", 0
}

// selectEdge is home and end: the selection goes to the first place it rests, or
// the last, from the search bar as from anywhere on the grid. They are the places
// tab walks between, so the first is where tab goes from the bar and the last is
// where tab stops; on a dashboard that groups its tiles one of them can be the
// header of a section, and a folded section at the end is where the selection
// has to rest since the tiles it holds are on no row.
//
// Not g and G, which a person coming from a pager would try: a letter answers as
// a command only once a tile holds the selection, and on the search bar it is
// the first letter of a query, so those two would have worked on some screens of
// the dashboard and typed on the others. Home and end are no letters of any
// query, and answer wherever the dashboard answers keys at all.
func (m *Model) selectEdge(last bool) {
	defer m.clampScroll()
	stops := m.stops()
	if len(stops) == 0 {
		return
	}
	edge := stops[0]
	if last {
		edge = stops[len(stops)-1]
	}
	m.selectStop(edge)
	m.goalCol = edge.col
}

// returned is what a tile's capability handed back, before cleaning.
type returned struct {
	view view.View
	err  *view.Error
}

// rawKey names a tile in Model.tileRaw: its key and the values it runs with.
// The key alone is not a tile's identity — a config listing obj.get twice,
// once per host, holds two tiles of one key (tileIndexFor says so), and
// keyed by it alone each panel drew whichever host had answered last.
// fmt prints a map with its keys sorted, so equal values name one tile.
func rawKey(t tile) string { return t.key() + "\x00" + fmt.Sprint(t.values) }

// asReturned is tile i carrying what its capability returned in place of the
// cleaned copy the tile holds, for cli.Render, which draws the panel and
// cleans its own. A tile with nothing on it yet is left as it is, whatever an
// earlier tile of the same name once returned: it is drawn as loading.
//
// A tile can hold an answer with nothing returned kept for its inputs:
// rebuildTiles carries a panel over by its key alone, so an entry edited in
// another terminal keeps the answer to its old inputs, and what its
// capability returned stays filed under those. That tile is drawn from its
// cleaned copy, which is safe — Terminal gives the same answer twice, so
// cleaning it again changes nothing — and drawing is all the fallback is
// safe for. Anything that hands a value on reads tileReturned, which has no
// fallback.
func (m Model) asReturned(i int) tile {
	t := m.tiles[i]
	if t.view == nil && t.err == nil {
		return t
	}
	if r, ok := m.tileRaw[rawKey(t)]; ok {
		t.view, t.err = r.view, r.err
	}
	return t
}

// tileReturned is the view tile i's capability returned for the inputs the
// tile runs with, and nil while it holds none — for the copy key, which puts
// a value on the clipboard, and the footer's offer of it. The cleaned copy
// asReturned falls back to is a display spelling: a password holding an
// override would be copied as the backslash-u escape Terminal writes for
// it, a string that was never the password. Nothing to copy until the tile
// answers again is the honest answer, and the footer says so by offering
// nothing.
func (m Model) tileReturned(i int) view.View {
	t := m.tiles[i]
	if t.view == nil && t.err == nil {
		return nil
	}
	return m.tileRaw[rawKey(t)].view
}

// tileContentLines renders one tile's body at width, full length — the
// natural content driving both how tall its row needs to be (tileRowHeight)
// and what renderTile then clamps to whatever height that row settled on.
func tileContentLines(t tile, width int) []string { return tileLines(t, width, false) }

// tileLines is tileContentLines for a tile that may be the selection, which
// matters to one sentence (keyPromised). The row's height is sized from the
// tile unselected, which is the longer of the two, so selecting a tile never
// asks its row for room it was not given.
func tileLines(t tile, width int, selected bool) []string {
	inner := width - 4
	body := ""
	switch {
	case t.err != nil:
		if say, quiet := quietSentence(t); quiet {
			body = theme.Subtle.Render(ansi.Wordwrap(say, max(inner, 1), ""))
			break
		}
		var buf bytes.Buffer
		_ = cli.RenderError(&buf, t.err, cli.Options{Format: cli.Pretty, Width: inner})
		body = buf.String()
	case t.view != nil:
		body = tileBody(keyPromised(t, selected), inner)
	default:
		body = theme.Subtle.Render("loading…")
	}
	return strings.Split(strings.TrimRight(body, "\n"), "\n")
}

// keyPromised is the tile's view with one sentence made true while the tile is
// not the selection: an empty list that says "press a to add one", where `a` is
// one of the keys the capability declares for its own tile.
//
// Those keys answer on the selected tile (dispatch.go, selectedAction) and
// nowhere else, and until a tile is selected the search bar holds every letter.
// The sentence is read first on the first screen of a fresh install, where
// nothing is selected, and "press a" there started a search for the letter a.
// The plugin cannot say it right for both places: the same sentence is the whole
// of the opened list, where the key answers at once, so the tile qualifies it
// when it is drawn.
//
// Keyed on the keys the capability declares and not on the shape of a sentence,
// so a sentence naming a key nothing answers is left as its plugin wrote it. A
// sentence that already says to select is left as it is as well: a plugin that
// words it for the tile has nothing to be told twice, and "select a tile, then
// select this tile, then press a" is no instruction.
//
// The sentence is read, and not parsed: a capability whose declared key is `a`
// and whose empty list says "press a name to narrow" is read as naming the key,
// and the Action doc says the wording is the plugin's to keep clear of that.
func keyPromised(t tile, selected bool) view.View {
	tbl, ok := t.view.(view.Table)
	if selected || !ok || len(tbl.Rows) > 0 || saysToSelect(tbl.Empty) {
		return t.view
	}
	for _, a := range t.cap.Actions {
		at, named := pressIndex(tbl.Empty, a.Key)
		if !named {
			continue
		}
		lead := "select this tile, then "
		if tbl.Empty[at] == 'P' {
			lead = "Select this tile, then "
		}
		tbl.Empty = tbl.Empty[:at] + lead + "press " + tbl.Empty[at+len(pressVerb):]
		return tbl
	}
	return t.view
}

// pressVerb is how an empty list's sentence tells the reader to press a key.
const pressVerb = "press "

// pressIndex is where text tells the reader to press key: the verb in either
// case, on a word boundary, then the key, which does not run into the word that
// follows it when it ends in a letter. A key that is a symbol, `+`, has no word
// to run into and is named wherever it stands, which is what a `\b` after it
// could not say: no boundary lies between a symbol and the space beside it.
//
// Scanned by hand, because the rewrite runs for every empty tile on every frame
// and a pattern compiled from each declared key each time was the most
// expensive thing a frame did for a tile with nothing in it. The characters on
// either side are read as characters: a dash or an ellipsis before the verb is
// punctuation, and a letter of another script after the key is a word.
func pressIndex(text, key string) (int, bool) {
	if key == "" {
		return 0, false
	}
	lastOfKey, _ := utf8.DecodeLastRuneInString(key)
	for i := 0; i+len(pressVerb)+len(key) <= len(text); i++ {
		if !strings.EqualFold(text[i:i+len(pressVerb)], pressVerb) || !strings.HasPrefix(text[i+len(pressVerb):], key) {
			continue
		}
		if before, _ := utf8.DecodeLastRuneInString(text[:i]); i > 0 && isWordRune(before) {
			continue
		}
		after := i + len(pressVerb) + len(key)
		if next, _ := utf8.DecodeRuneInString(text[after:]); after < len(text) && isWordRune(lastOfKey) && isWordRune(next) {
			continue
		}
		return i, true
	}
	return 0, false
}

// saysToSelect is whether text already tells the reader to select something: the
// word, and not "selected" or "selection", which a sentence about an empty list
// has its own reasons to use.
func saysToSelect(text string) bool {
	const verb = "select"
	lower := strings.ToLower(text)
	for from := 0; ; {
		i := strings.Index(lower[from:], verb)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(verb)
		before, _ := utf8.DecodeLastRuneInString(lower[:start])
		after, _ := utf8.DecodeRuneInString(lower[end:])
		if (start == 0 || !isWordRune(before)) && (end == len(lower) || !isWordRune(after)) {
			return true
		}
		from = end
	}
}

// isWordRune is a character that is part of a word: a letter or a digit of any
// script, or an underscore.
func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// tileRowHeight is how tall a row needs to be to show t in full, clamped to
// [tileMinHeight, tileHeight] — the two ends of the range that already
// existed (a fully-shown tile never grew past tileHeight; nothing had a
// floor before because every row was the same fixed height).
func tileRowHeight(t tile, width int) int {
	h := len(tileContentLines(t, width)) + 2 // + top/bottom border
	return min(max(h, tileMinHeight), tileHeight)
}

// renderTile draws one preview panel at the given height — its row's height,
// shared by every tile in that row so neighbors still align exactly the way
// they did under the old fixed tileHeight. The panel primitive guarantees
// exact width × height cells, so overflowing content can never make one tile
// taller than its row.
func renderTile(t tile, width, height int, selected, heavy bool) string {
	lines := tileLines(t, width, selected)
	preview := height - 2
	if len(lines) > preview {
		lines = lines[:max(preview-1, 0)]
		lines = append(lines, theme.Subtle.Render("… enter for details"))
	}
	// The profile beside the capability, not in a corner the panel drops
	// when it is short of room: two tiles of one capability against two
	// clusters are the same panel twice without it, and a tile about
	// production is the one panel whose name must not be the first thing
	// to go.
	head := panelHead{Title: t.cap.ID, Note: t.profile, NoteColor: t.color, Heavy: heavy}
	// A tile that says calls are parked is the one panel on the screen with a
	// clock on it, and it is not always on the first row of a dashboard somebody
	// arranged: the badge is in the border, where the eye is on the way past, and
	// not only in a line the tile has to be read to find.
	if n := waitingCalls(t.view); n > 0 {
		head.Right, head.Attention = fmt.Sprintf("● %d waiting", n), true
	}
	return panel(head, strings.Join(lines, "\n"), width, height, selected)
}

// searchResults filters the registry live, best match first. Every match is
// returned — the window is a rendering decision, made once, in
// renderSearchTile.
func (m Model) searchResults() []plugin.Capability {
	// Straight from the registry rather than from the browse list: the two
	// answer the same question and only one of them has section headers in it.
	caps := m.reg.Capabilities()
	items := make([]searchItem, len(caps))
	for i, c := range caps {
		items[i] = itemOf(c)
	}
	found := rankItems(m.query, items)
	out := make([]plugin.Capability, len(found))
	for n, i := range found {
		out[n] = caps[i]
	}
	return out
}

// searchHint answers a query that names something that is not on the
// dashboard's search because it is not a capability, or is one that is not
// running: a first-run person typing pg or postgres into a dashboard that has
// neither is told what to run, not shown "no matches" or, worse, only the one
// unrelated capability whose name contains those letters.
//
// For a plugin it is the sentence the command line gives for the same word
// (plugindist.WordHint), so the two surfaces cannot disagree: an installed
// plugin that is waiting for approval is told to be approved and one that failed
// to start is told where the reason is, and neither is sent to an install that
// answers "already installed". It reads the registry for what is running, so a
// plugin that is never gets told to install itself.
//
// The plugin is the first word of the query, or the part before its first dot:
// a person reaching for a capability types `redis.key.list` or `mysql query`,
// and only the bare name was recognised, so those were answered with fuzzy
// matches for unrelated capabilities and, for `mysql query`, "no matches".
func (m Model) searchHint() string {
	word := strings.TrimSpace(m.query)
	if i := strings.IndexAny(word, " ."); i > 0 {
		word = word[:i]
	}
	if word == "" {
		return ""
	}
	if hint := panesHint(word); hint != "" {
		return hint
	}
	caps := m.reg.Capabilities()
	hint, _ := plugindist.WordHint(word, func(namespace string) bool {
		return slices.ContainsFunc(caps, func(c plugin.Capability) bool {
			return plugin.Namespace(c.ID) == namespace
		})
	}, m.untrusted, m.failed)
	return hint
}

// panesHint says where the two things a first-run person has to set up live,
// for the words they type for them. Plugins and profiles are panes and not
// capabilities, so the search found nothing for "plugin" or "profile" and, for
// "install plugin", the one capability whose summary says both, which reads as
// the install feature and is a package manager. The panes open on a tile
// (typing.go: on the search bar a letter is the query), which is the part
// nobody can guess from the bar that says "type to search".
//
// The four words are the ones a first-run person reaches for, singular or
// plural. `index` is not one of them: it names a plugin index, which is a
// command and not a pane, and it is the first word of a search that is about
// something else entirely — `index bloat` on a database — where the line would
// answer a question nobody asked and take one of the few result rows.
func panesHint(word string) string {
	switch strings.TrimSuffix(strings.ToLower(word), "s") {
	case "plugin", "install":
		return "plugins: esc, select a tile, press p · or `rta plugin install <name>`"
	case "profile", "environment":
		return "profiles: esc, select a tile, press f · or `rta profile set <name>`"
	}
	return ""
}

// searchSlots is how many lines the open box has for what it answers: a fifth of
// the terminal's height, at least searchMatches and at most searchMatchesMax. It
// was three at every size, so a terminal sixty lines high showed three results
// and a hint over forty lines of tiles the person had stopped looking at: while
// the box is open the search is what they are doing, and the count in its
// border, which says how many more there are, is the part that stays honest at
// any size.
//
// It is read off the height the model draws in, and everything that measures
// the dashboard under the box reads the same number — the row budget, the hit
// test, the scroll window — through searchHeight.
func (m Model) searchSlots() int {
	return min(max(m.height/searchHeightShare, searchMatches), searchMatchesMax)
}

// searchTileHeight is the open box: its result lines and what surrounds them.
func (m Model) searchTileHeight() int { return searchChrome + m.searchSlots() }

// searchRows is how many matches the box shows right now: searchSlots, and one
// fewer while a hint keeps a line of its own. A query that names a plugin that is
// not running scores the capabilities of every other plugin on the words that are
// left (`qdrant.collection.list` found 56), and with the hint drawn only in a
// window with room, those unrelated rows pushed out the one line that said why
// qdrant was not among them.
func (m Model) searchRows() int {
	if m.searchHint() != "" {
		return m.searchSlots() - 1
	}
	return m.searchSlots()
}

// searchWindow returns the visible slice of matches and where it starts,
// keeping the selection inside it. The offset is derived rather than stored,
// so it cannot drift out of step with a selection the keys just moved.
func (m Model) searchWindow(results []plugin.Capability) ([]plugin.Capability, int) {
	rows := m.searchRows()
	if len(results) <= rows {
		return results, 0
	}
	sel := min(m.searchSel, len(results)-1)
	// The window follows the selection by the shortest move that keeps it
	// visible, which is one line per keypress once you are past the last row.
	first := max(0, sel-rows+1)
	return results[first : first+rows], first
}

// searchOpen reports whether the search has the keyboard or a query to show,
// which is when its box is drawn instead of the one-line bar.
func (m Model) searchOpen() bool { return m.searchEditing || m.query != "" }

// searchHeight is the lines the search takes right now, which everything that
// measures the tile rows below it — the budget, the hit test, the scroll
// window — has to agree on.
func (m Model) searchHeight() int {
	if m.searchOpen() {
		return m.searchTileHeight()
	}
	return searchBarHeight
}

// renderSearchBar is the search as it sits when nobody is using it: one line,
// with the inventory it would search. Selecting it moves the glyph and the
// colour, and not a border, because there is none to move — the glyph is what
// carries the selection on a terminal that shows no colour.
func (m Model) renderSearchBar(width int, selected bool) string {
	info := m.searchInfo + " — " + m.searchStartHint()
	glyph, text := theme.AccentTxt.Render(" ⌕ "), theme.Subtle.Render(info)
	if selected {
		glyph, text = theme.AccentTxt.Render(" ❯ "), theme.Key.Render(info)
	}
	return ansi.Truncate(glyph+text, max(width, 1), "…")
}

// searchStartHint says what starts a search from where the selection is: a
// letter when the bar holds it (typing.go hands letters to the bar only then),
// the slash otherwise, because on a tile a letter is that tile's command.
func (m Model) searchStartHint() string {
	if m.lettersAreQuery() {
		return "type to search"
	}
	return "press / to search"
}

// renderSearchTile draws the full-width live search box: a query line and a
// window over the matches, updating on every keystroke.
func (m Model) renderSearchTile(width int, selected bool) string {
	prompt := theme.AccentTxt.Render("❯ ")
	switch {
	case m.searchEditing:
		prompt += m.query + theme.AccentTxt.Render("▌")
	case m.query != "":
		prompt += m.query
	default:
		prompt += theme.Subtle.Render(m.searchInfo)
	}

	lines := []string{prompt}
	results := m.searchResults()
	window, first := m.searchWindow(results)
	inner := width - 4 // what panel() leaves for a body line
	for i, c := range window {
		marker, id := "  ", theme.Subtle.Render(c.ID)
		if first+i == min(m.searchSel, len(results)-1) {
			marker, id = theme.AccentTxt.Render("❯ "), theme.Key.Render(c.ID)
		}
		line := marker + id + theme.Subtle.Render("  "+c.Summary)
		// A caret on the first and last visible line says the list continues
		// in that direction — the same language the tile grid uses. It sits
		// against the right edge, where a scrollbar would be, so it reads as
		// a scroll position rather than as part of the summary.
		more := " "
		switch {
		case i == 0 && first > 0:
			more = theme.Subtle.Render("↑")
		case i == len(window)-1 && first+len(window) < len(results):
			more = theme.Subtle.Render("↓")
		}
		// What it is allowed to do rides in the same right-hand column the
		// catalogue uses, so the answer is in the same place on both screens.
		if inner > 0 {
			line = rightAlign(line, permissionLabel(c)+" "+more, inner)
		}
		lines = append(lines, line)
	}
	switch hint := m.searchHint(); {
	case hint != "":
		lines = append(lines, theme.Subtle.Render(ansi.Truncate("  "+hint, max(inner, 1), "…")))
	case m.query != "" && len(results) == 0:
		lines = append(lines, theme.Subtle.Render("  no matches"))
	}

	// What starts a search depends on whether the bar holds the selection:
	// typed letters are the query only then (typing.go), and on a tile they are
	// that tile's commands, so the hint says the key that works from there.
	right := "press /"
	if m.lettersAreQuery() {
		right = "type to search"
	}
	if m.searchEditing {
		right = "↑↓ pick · enter run · + add tile · esc clear"
	}
	// The count is the honest part: it says three of eleven, so a match that
	// is not on screen is a known thing rather than a missing one.
	if n := len(results); n > m.searchRows() {
		right = fmt.Sprintf("%d/%d · ", min(m.searchSel, n-1)+1, n) + right
	}
	return panel(panelHead{Title: "⌕ search", Right: right, Heavy: m.noColor},
		strings.Join(lines, "\n"), width, m.searchTileHeight(), selected)
}

// dashFooterItems is the dashboard's hint bar: the selected tile's own actions
// lead, then navigation, all from the one vocabulary in keys.go.
func (m Model) dashFooterItems() []hintItem {
	if m.searchEditing {
		return searchEditingItems()
	}
	if m.lettersAreQuery() {
		return searchIdleItems()
	}
	items := []hintItem{}
	own := m.dashOwnItems()
	if m.selected > 0 && m.selected < len(m.tiles) {
		t := m.tiles[m.selected]
		for _, a := range m.offeredTileActions(m.selected) {
			items = append(items, action(a.key, a.label))
		}
		if hint, ok := copyHint(t.cap, m.tileReturned(m.selected)); ok {
			items = append(items, hint)
		}
		if t.withdraws() {
			for i, h := range own {
				if h.display == bindHide.display {
					own[i] = labelled(bindHide, "remove")
				}
			}
		}
	}
	return append(items, own...)
}

// searchEditingItems is the bar while the search box holds the keyboard, which
// is a different vocabulary from the dashboard's: `q`, `f`, `p`, `t`, `H`, `b`
// and the brackets are letters of a query there, so advertising them told
// somebody mid-search that `q quit` and `p plugins` work when they only type.
// What the box answers is what updateSearch handles, and nothing else.
func searchEditingItems() []hintItem {
	return []hintItem{
		{
			display: "↑↓", label: "pick", rank: rankPrimary,
			keys: []string{"up", "down", "ctrl+p", "ctrl+n", "tab", "pgup", "pgdown"},
		},
		labelled(bindOpen, "run"), item(bindAdd), labelled(bindBack, "clear"),
		{display: "ctrl+c", label: "quit", rank: rankLeave, keys: []string{"ctrl+c"}},
	}
}

// dashOwnItems are the keys the dashboard answers to whatever is selected:
// moving around, the panes, and the ways out. Separate from dashFooterItems
// because the claimed-key set in offeredTileActions has to be exactly these
// and not the tile actions standing beside them.
//
// A dashboard with sections adds the two keys that fold, and nothing else is
// different about one that has none. On a header, enter folds instead of
// opening — there is no tile to open — and H is not offered, since it hides a
// tile and a header is not one.
func (m Model) dashOwnItems() []hintItem {
	open := labelled(bindOpen, "details")
	if m.selected < 0 {
		open = hintItem{
			display: bindOpen.display, label: m.foldVerb(m.header), rank: bindOpen.rank,
			keys: []string{"enter", " ", "space"},
		}
	}
	items := []hintItem{
		item(bindSelect), open,
		item(bindProfile), item(bindPlugin), item(bindTheme),
		item(bindMove),
	}
	if m.selected >= 0 {
		items = append(items, item(bindHide))
	}
	items = append(items, item(bindView), item(bindAdd))
	items = append(items, m.unlockItem())
	if m.hasSections() {
		items = append(items, item(bindFold), item(bindFoldAll))
	}
	return append(items, item(bindBrowse), item(bindSearch), item(bindQuit))
}

// offeredTileActions are the selected tile's actions the dashboard can
// actually run, which is not all of the ones it declares.
//
// dashboardKeys answers `t`, `f`, `p` and the rest of the pane keys in its own
// switch and returns handled, so the `default:` branch that consults
// selectedAction is never reached for them. note.list declares `t to-do/note`
// and the dashboard claims `t` for the theme form, and the bar used to print
// both — `t to-do/note` among the tile's actions and `t theme` among the pane
// keys, two verbs for one key on two lines of the same footer, with only the
// theme form ever opening.
//
// The action is not lost, only the tile's one-key shortcut to it: `enter`
// opens the tile and the same action is there on the result pane, which claims
// no pane keys of its own. Filtering here rather than moving note.list's key
// off `t` keeps the note toggle spelled the same way in the list and on a
// note's own page, which is the point of one table driving every surface.
//
// The claimed set is derived from dashOwnItems rather than listed again here,
// so it cannot drift from the bar when a pane key moves.
func (m Model) offeredTileActions(i int) []capAction {
	claimed := map[string]bool{}
	for _, h := range m.dashOwnItems() {
		for _, k := range h.keys {
			claimed[k] = true
		}
	}
	var offered []capAction
	for _, a := range m.tiles[i].actions {
		if a.key == "enter" {
			continue // enter opens the tile itself
		}
		if claimed[a.key] {
			continue
		}
		offered = append(offered, a)
	}
	return offered
}

// dashFooter renders it.
//
// A method rather than an expression inside dashboardView because the row
// budget has to know how tall it came out — a footer that wraps to two lines
// and a grid that still reserves one is how a tile row ends up drawn under the
// terminal's last line.
func (m Model) dashFooter() string { return m.footerFor(modeDashboard) }

// dashboardView lays the search bar and uniform preview tiles out.
func (m Model) dashboardView() string {
	header := theme.Title.Render(" rta") + theme.Subtle.Render("  dashboard")
	// Where am I. The one fact worth spending header space on: every tile below
	// is showing that environment, and every command run from this screen lands
	// in it. A person who has to press a key to find that out will find it out
	// after the command.
	if badge := m.activeBadge(); badge != "" {
		header += " " + m.paintBadge(badge)
	}
	// And which screen of the dashboard this is, when it is not the one every
	// machine has: tiles below are the view's, and H, + and [ ] write to it.
	//
	// The name is a key in a config file, which a drop-in or a repository can
	// bring, and so it is shown as every listed name is (plugin.ListedName):
	// as it is when it reads as itself, quoted with each character a person
	// would not see written out when it does not. Drawn raw, a name carried an
	// escape that coloured the rest of the line, an OSC 8 that made the word
	// beside it a link, a one-byte CSI the terminal acts on, and a newline that
	// started a second header line.
	if m.view != "" {
		header += theme.Subtle.Render("  view " + plugin.ListedName(m.view))
	}
	// And whether the secret store is open in this process — the other fact
	// about "where am I" that changes what the next keystroke can reach.
	if badge := m.storeBadge(); badge != "" {
		header += " " + badge
	}
	footer := m.dashFooter()
	if len(m.tiles) == 0 {
		return header + "\n\n" + theme.Subtle.Render("  no tiles available") + "\n" + footer
	}

	search := m.renderSearchBar(m.width, m.selected == 0)
	if m.searchOpen() {
		search = m.renderSearchTile(m.width, m.selected == 0)
	}

	rows := m.tileRows()
	if len(rows) == 0 {
		frame := header + "\n" + search + "\n" + footer
		// Said when there is room, for a dashboard with nothing under the search
		// bar looks like one that failed to load, and its footer offers to
		// select, move and hide what is not there.
		if m.height-lipgloss.Height(frame) >= 1 {
			note := "  no tiles — p brings hidden ones back, + adds one"
			frame = header + "\n" + search + "\n" + theme.Subtle.Render(ansi.Truncate(note, max(m.width, 1), "…")) + "\n" + footer
		}
		return frame
	}

	if !m.tilesFit() {
		return m.tooSmallFrame(header, search, footer)
	}

	// Window the rows to the screen; the selection-driven scroll offset is
	// maintained by clampScroll. Markers show there is more above/below.
	first, last := m.rowWindow(len(rows))
	heights := m.drawnHeights(first, last)
	rendered := make([]string, 0, last-first)
	for r := first; r < last; r++ {
		if rows[r].isHeader() {
			rendered = append(rendered, m.renderSectionHeader(rows[r].header, m.onHeader(rows[r].header.name)))
			continue
		}
		parts := make([]string, 0, 2*len(rows[r].cells))
		for _, i := range rows[r].cells {
			if len(parts) > 0 {
				// A one-cell gap column between tiles — the same gap tileAt
				// divides by.
				parts = append(parts, " ")
			}
			parts = append(parts, renderTile(m.asReturned(i), m.tileWidth(i), heights[r-first], i == m.selected, m.noColor))
		}
		rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}
	switch {
	case first > 0 && last < len(rows):
		header += m.moreHint(header, "↕")
	case first > 0:
		header += m.moreHint(header, "↑")
	case last < len(rows):
		header += m.moreHint(header, "↓")
	}
	return aboveFooter(header+"\n"+search+"\n"+strings.Join(rendered, "\n"), footer, m.height)
}

// tooSmallNote is what the tile area says when no row of tiles fits in it.
const tooSmallNote = "terminal too small, enlarge it to see tiles"

// tooSmallNotes are what it says when the terminal is too narrow for that, whole
// words at a time: the sentence, the part of it that says what is the matter, and
// the least that does. A sentence cut where the edge falls is "terminal too smal…"
// on the terminals that most need it.
var tooSmallNotes = []string{tooSmallNote, "terminal too small", "too small"}

// tooSmallLine is the longest of tooSmallNotes that fits, drawn muted and
// indented as the other notes under the search are.
func (m Model) tooSmallLine() string {
	for _, note := range tooSmallNotes {
		if line := "  " + note; m.width <= 0 || lipgloss.Width(line) <= m.width {
			return theme.Subtle.Render(line)
		}
	}
	return theme.Subtle.Render(ansi.Truncate("  "+tooSmallNotes[len(tooSmallNotes)-1], max(m.width, 1), "…"))
}

// tooSmallFrame is the dashboard on a terminal with no room for a row of tiles:
// the header, the search and the footer, and one muted line where the tiles
// would be, when the terminal has a line to spare, that says why they are not
// there. A dashboard that draws nothing under its search looks like one that
// failed to load, which is what the note for a dashboard with no tiles is for.
func (m Model) tooSmallFrame(header, search, footer string) string {
	top := header + "\n" + search
	if m.height-lipgloss.Height(top+"\n"+footer) >= 1 {
		top += "\n" + m.tooSmallLine()
	}
	return aboveFooter(top, footer, m.height)
}

// moreHint is the header's pointer to the rows off the screen, in the direction
// of arrow: the arrow and the word, or the arrow alone when the word does not
// fit after what the header already says, or nothing when not even that does.
// The terminal cuts a line at its edge wherever the edge falls, and "↓ mo" says
// less than the arrow did.
func (m Model) moreHint(header, arrow string) string {
	for _, hint := range []string{"  " + arrow + " more", "  " + arrow} {
		if m.width <= 0 || lipgloss.Width(header)+lipgloss.Width(hint) <= m.width {
			return theme.Subtle.Render(hint)
		}
	}
	return ""
}

// aboveFooter puts footer under top, and cuts top from its bottom when the two
// together are taller than the terminal. The grid budgets the tile rows for
// the room there is, but it never draws a row shorter than its borders and the
// line that says there is more, so on a terminal about ten rows high the
// header, the search bar and that row already filled it and the footer, which
// is where "q quit" is, was the part that fell off. A clipped tile is the
// smaller loss.
func aboveFooter(top, footer string, height int) string {
	if height > 0 {
		lines := strings.Split(top, "\n")
		if room := max(height-lipgloss.Height(footer), 0); len(lines) > room {
			if room == 0 {
				return footer
			}
			top = strings.Join(lines[:room], "\n")
		}
	}
	return top + "\n" + footer
}
