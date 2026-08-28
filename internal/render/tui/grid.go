package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Laying the dashboard out and painting it: how tiles become rows and
// columns at a width, which rows fit, and what one tile renders as —
// including tile zero, the search bar.

// grid describes the capability-tile geometry below the full-width search
// bar — one source of truth for painting and mouse hit-testing.
type grid struct {
	cols, tileW int
}

// minColWidth is the narrowest a tile column gets before its summaries stop
// being readable. It is derived from where the dashboard already stood: two
// columns from 80 cells up, which is 39 cells and a gap.
const minColWidth = 40

// maxCols caps the automatic grid. Past four the tiles are narrow enough that
// the eye stops scanning columns and starts hunting, and a dashboard you hunt
// through is a dashboard you stop opening.
const maxCols = 4

func (m Model) grid() grid {
	// The dashboard was two columns at every width, so a 200-cell terminal
	// drew two 100-cell tiles of a six-line summary — half the screen spent
	// on whitespace, with the tiles that did not fit pushed below the fold.
	cols := max(min(m.width/minColWidth, maxCols), 1)
	if n := m.dash.Columns; n > 0 {
		cols = max(n, 1)
	}
	// A configured column count still has to fit: honouring columns: 4 on a
	// 60-cell terminal draws four 14-cell tiles, which is not what anybody
	// meant by it.
	cols = max(min(cols, m.width/minColWidth), 1)
	// One gap cell between columns; hit-testing divides by tileW+1.
	return grid{cols: cols, tileW: (m.width - (cols - 1)) / cols}
}

// span is how many columns tile i occupies at the current grid width: what
// the capability declared it needs, what the config overrode it to, clamped
// to a grid that may be narrower than either.
func (m Model) span(i int) int {
	g := m.grid()
	if i <= 0 || i >= len(m.tiles) {
		return 1
	}
	t := m.tiles[i]
	if t.span > 0 {
		return max(min(t.span, g.cols), 1)
	}
	if t.cap.MinWidth <= 0 {
		return 1
	}
	// Each extra column brings its own width plus the gap cell before it.
	n := 1
	for n < g.cols && n*g.tileW+(n-1) < t.cap.MinWidth {
		n++
	}
	return n
}

// tileRows groups the capability tiles into the rows they are drawn on: a
// wide tile takes a row to itself, the rest fill the grid.
//
// This is the one place that decides which tile sits on which row. It used
// to be `i/cols` arithmetic repeated across rendering, hit-testing, scroll
// paging and selection movement — four copies of one rule, correct only for
// as long as every row held exactly cols tiles, and all four of which had to
// be found and changed together the moment one did not.
func (m Model) tileRows() [][]int {
	g := m.grid()
	var rows [][]int
	var cur []int
	used := 0
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, cur)
			cur, used = nil, 0
		}
	}
	for i := 1; i < len(m.tiles); i++ {
		w := m.span(i)
		if used+w > g.cols {
			flush()
		}
		cur = append(cur, i)
		used += w
		if used >= g.cols {
			flush()
		}
	}
	flush()
	return rows
}

// rowSpan is how many columns a row's tiles claim between them.
func (m Model) rowSpan(cells []int) int {
	n := 0
	for _, i := range cells {
		n += m.span(i)
	}
	return n
}

// tileWidth is how wide one tile is drawn: its span in grid columns, plus
// whatever a short row has left over.
//
// The leftover matters. A wide tile forces the row before it to close early,
// so with nine tiles in two columns the eighth used to be drawn at half
// width with dead space beside it and nothing to explain the hole. A row that
// does not fill the grid shares out what is left, so a short row reads as a
// deliberate arrangement instead of a rendering accident.
func (m Model) tileWidth(i int) int {
	g := m.grid()
	cells := m.rowOf(i)
	if len(cells) == 0 {
		return g.tileW
	}
	spare := 0
	if left := g.cols - m.rowSpan(cells); left > 0 {
		spare = left / len(cells)
	}
	widths := make([]int, len(cells))
	used := len(cells) - 1 // the gap cell between tiles
	for k, idx := range cells {
		sp := m.span(idx) + spare
		widths[k] = sp*g.tileW + (sp - 1)
		used += widths[k]
	}
	// The last tile absorbs whatever integer division left behind, so a row
	// is exactly the screen wide and its right edge lines up with every
	// other row's. Computing each width independently left a cell or two
	// unaccounted for at some terminal sizes, which reads as a wobble down
	// the right-hand side that nothing on screen explains.
	widths[len(cells)-1] += m.width - used
	for k, idx := range cells {
		if idx == i {
			return max(widths[k], 1)
		}
	}
	return g.tileW
}

// rowOf returns the row tile i sits on.
func (m Model) rowOf(i int) []int {
	for _, cells := range m.tileRows() {
		if slices.Contains(cells, i) {
			return cells
		}
	}
	return nil
}

// rowAndCol locates a tile in the row layout.
func (m Model) rowAndCol(idx int) (int, int) {
	for r, row := range m.tileRows() {
		for c, i := range row {
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
// varies row to row instead of being one constant for the whole grid.
func (m Model) rowHeights() []int {
	rows := m.tileRows()
	heights := make([]int, 0, len(rows))
	for _, row := range rows {
		h := tileMinHeight
		for _, i := range row {
			h = max(h, tileRowHeight(m.tiles[i], m.tileWidth(i)))
		}
		heights = append(heights, h)
	}
	return heights
}

// visibleRowCount is how many rows starting at from fit within avail lines,
// given each row's own height — the shared arithmetic tileAt, dashRowsVisible
// and dashboardView all need to agree on, now that a row's height is no
// longer one constant they could each compute independently.
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
// height — offset by the scroll position.
func (m Model) tileAt(x, y int) int {
	if y < 1 {
		return -1
	}
	if y < 1+searchTileHeight {
		return 0
	}
	rows := m.tileRows()
	heights := m.rowHeights()
	yy := y - 1 - searchTileHeight
	row := -1
	for i := m.scroll; i < len(heights); i++ {
		if yy < heights[i] {
			row = i - m.scroll
			break
		}
		yy -= heights[i]
	}
	if row < 0 || row >= m.dashRowsVisible() {
		return -1 // footer area below the last visible tile row
	}
	// Walk the row by drawn width. Dividing x by one column width only ever
	// worked while every tile was one column wide, and the special case for
	// a single full-width tile was that assumption's one exception rather
	// than a rule — a two-column tile beside a one-column one landed every
	// click in the row on the wrong tile.
	cells := rows[row+m.scroll]
	for _, i := range cells {
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

// dashRowsVisible reports how many tile rows fit under header + search bar,
// starting from the current scroll position.
func (m Model) dashRowsVisible() int {
	if m.height <= 0 {
		return 1 << 30 // size unknown yet: render everything
	}
	avail := m.height - 1 - lipgloss.Height(m.dashFooter()) - searchTileHeight
	return visibleRowCount(m.rowHeights(), m.scroll, avail)
}

// dashRows is the total number of capability-tile rows at the current width.
func (m Model) dashRows() int { return len(m.tileRows()) }

// clampScroll keeps the selected tile visible and the offset in bounds. The
// search bar never scrolls; capability tiles window selection-driven.
func (m *Model) clampScroll() {
	visible := m.dashRowsVisible()
	m.scroll = min(m.scroll, max(0, m.dashRows()-visible))
	if m.selected == 0 {
		return
	}
	selRow, _ := m.rowAndCol(m.selected)
	if selRow < 0 {
		return
	}
	if selRow < m.scroll {
		m.scroll = selRow
	}
	if selRow >= m.scroll+visible {
		m.scroll = selRow - visible + 1
	}
}

// moveSelection shifts the selected tile by (dx, dy): the search bar sits
// above the grid, capability tiles move on it.
func (m *Model) moveSelection(dx, dy int) {
	defer m.clampScroll()
	if m.selected == 0 {
		if dy > 0 || dx > 0 {
			m.selected = min(1, len(m.tiles)-1)
		}
		m.goalCol = 0
		return
	}
	// Left and right walk the tiles in order, which is what they look like
	// they do. Up and down move between rows and keep the column, which is
	// only the same thing while every row is the same length — and a
	// full-width tile makes one of them shorter.
	if dx != 0 {
		if next := m.selected + dx; next >= 1 && next < len(m.tiles) {
			m.selected = next
		}
		// Moving sideways is choosing a column, so it sets the one vertical
		// movement will aim for.
		_, m.goalCol = m.rowAndCol(m.selected)
		return
	}
	if dy == 0 {
		return
	}
	rows := m.tileRows()
	row, col := m.rowAndCol(m.selected)
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
		m.selected = 0 // up from the top row lands on search
		return
	}
	if target >= len(rows) {
		return
	}
	m.goalCol = col
	m.selected = rows[target][min(col, len(rows[target])-1)]
}

// tileContentLines renders one tile's body at width, full length — the
// natural content driving both how tall its row needs to be (tileRowHeight)
// and what renderTile then clamps to whatever height that row settled on.
func tileContentLines(t tile, width int) []string {
	inner := width - 4
	body := ""
	switch {
	case t.err != nil:
		var buf bytes.Buffer
		_ = cli.RenderError(&buf, t.err, cli.Options{Format: cli.Pretty, Width: inner})
		body = buf.String()
	case t.view != nil:
		var buf bytes.Buffer
		// Fill: a tile is drawn at the grid's width whether its content wants
		// it or not, so the slack belongs to the content rather than to the
		// space beside it.
		if err := cli.Render(&buf, t.view, cli.Options{Format: cli.Pretty, Width: inner, Fill: true}); err == nil {
			body = buf.String()
		}
	default:
		body = theme.Subtle.Render("loading…")
	}
	return strings.Split(strings.TrimRight(body, "\n"), "\n")
}

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
func renderTile(t tile, width, height int, selected bool) string {
	lines := tileContentLines(t, width)
	preview := height - 2
	if len(lines) > preview {
		lines = lines[:max(preview-1, 0)]
		lines = append(lines, theme.Subtle.Render("… enter for details"))
	}
	return panel(panelHead{Title: t.cap.ID}, strings.Join(lines, "\n"), width, height, selected)
}

// searchResults filters the registry live: prefix matches on the ID lead,
// then substring matches on ID or summary. Every match is returned — the
// window is a rendering decision, made once, in renderSearchTile.
func (m Model) searchResults() []plugin.Capability {
	q := strings.ToLower(strings.TrimSpace(m.query))
	if q == "" {
		return nil
	}
	var prefix, rest []plugin.Capability
	// Straight from the registry rather than from the browse list: the two
	// answer the same question and only one of them has section headers in it.
	for _, c := range m.reg.Capabilities() {
		switch {
		case strings.HasPrefix(strings.ToLower(c.ID), q):
			prefix = append(prefix, c)
		case strings.Contains(strings.ToLower(c.ID+" "+c.Summary), q):
			rest = append(rest, c)
		}
	}
	return append(prefix, rest...)
}

// searchWindow returns the visible slice of matches and where it starts,
// keeping the selection inside it. The offset is derived rather than stored,
// so it cannot drift out of step with a selection the keys just moved.
func (m Model) searchWindow(results []plugin.Capability) ([]plugin.Capability, int) {
	if len(results) <= searchMatches {
		return results, 0
	}
	sel := min(m.searchSel, len(results)-1)
	// The window follows the selection by the shortest move that keeps it
	// visible, which is one line per keypress once you are past the third.
	first := max(0, sel-searchMatches+1)
	return results[first : first+searchMatches], first
}

// renderSearchTile draws the full-width live search bar: a query line and a
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
	if m.query != "" && len(results) == 0 {
		lines = append(lines, theme.Subtle.Render("  no matches"))
	}

	right := "press /"
	if m.searchEditing {
		right = "↑↓ pick · enter run · esc clear"
	}
	// The count is the honest part: it says three of eleven, so a match that
	// is not on screen is a known thing rather than a missing one.
	if n := len(results); n > searchMatches {
		right = fmt.Sprintf("%d/%d · ", min(m.searchSel, n-1)+1, n) + right
	}
	return panel(panelHead{Title: "⌕ search", Right: right},
		strings.Join(lines, "\n"), width, searchTileHeight, selected)
}

// dashFooterItems is the dashboard's hint bar: the selected tile's own actions
// lead, then navigation, all from the one vocabulary in keys.go.
func (m Model) dashFooterItems() []hintItem {
	items := []hintItem{}
	if m.selected > 0 && m.selected < len(m.tiles) {
		t := m.tiles[m.selected]
		for _, a := range t.actions {
			if a.key == "enter" {
				continue // enter opens the tile itself
			}
			items = append(items, action(a.key, a.label))
		}
		if hint, ok := copyHint(t.cap.ID, t.view); ok {
			items = append(items, hint)
		}
	}
	return append(items,
		item(bindSelect), labelled(bindOpen, "details"),
		item(bindProfile), item(bindPlugin), item(bindTheme),
		item(bindMove), item(bindHide),
		item(bindBrowse), item(bindSearch), item(bindQuit),
	)
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
		header += theme.GoodText.Render(" " + badge)
	}
	footer := m.dashFooter()
	if len(m.tiles) == 0 {
		return header + "\n\n" + theme.Subtle.Render("  no tiles available") + "\n" + footer
	}

	search := m.renderSearchTile(m.width, m.selected == 0)

	rows := m.tileRows()
	heights := m.rowHeights()
	rendered := make([]string, 0, len(rows))
	for r, cells := range rows {
		parts := make([]string, 0, 2*len(cells))
		for _, i := range cells {
			if len(parts) > 0 {
				// A one-cell gap column between tiles — the same gap tileAt
				// divides by.
				parts = append(parts, " ")
			}
			parts = append(parts, renderTile(m.tiles[i], m.tileWidth(i), heights[r], i == m.selected))
		}
		rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}

	// Window the rows to the screen; the selection-driven scroll offset is
	// maintained by clampScroll. Markers show there is more above/below.
	first := min(m.scroll, max(0, len(rendered)-1))
	last := min(first+m.dashRowsVisible(), len(rendered))
	if len(rendered) == 0 {
		return header + "\n" + search + "\n" + footer
	}
	switch {
	case first > 0 && last < len(rendered):
		header += theme.Subtle.Render("  ↕ more")
	case first > 0:
		header += theme.Subtle.Render("  ↑ more")
	case last < len(rendered):
		header += theme.Subtle.Render("  ↓ more")
	}
	return header + "\n" + search + "\n" + strings.Join(rendered[first:last], "\n") + "\n" + footer
}
