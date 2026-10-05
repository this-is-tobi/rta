package tui

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/cli"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A tile is read at a glance, in a box a third of the screen wide, and the
// renderer that draws a result page does not know that. It draws a table as a
// grid while there is room for one and as one block per row when there is not,
// which is right for a page and wrong for a tile: a notebook of three entries
// took five lines each at 80 columns, so one and a half notes were on screen
// and the rest was "… enter for details". A path longer than the box wrapped
// over three lines of its own.
//
// The reshaping lives here, between the tile and the renderer, because it is a
// decision about the dashboard and not about the data: the capability returned
// one table, the page it opens on enter draws that same table, and the copy
// key reads what was returned. Only what the tile paints is changed.

const (
	// glanceGap is the cells between two columns of a glanced table.
	glanceGap = 2
	// glanceFlexMin is the narrowest the column that takes the slack is
	// squeezed to before a column is dropped instead: narrower and the text in
	// it is a stub nobody can read, which is no better than the column gone.
	glanceFlexMin = 10
	// glanceProse is how wide a text column's widest cell has to be to count as
	// prose, which is what is worth cutting. A column of short words is not cut
	// to ten cells, it is kept or dropped whole.
	glanceProse = 16
)

// tileBody draws a tile's view at the width its box leaves for content.
func tileBody(v view.View, inner int) string {
	if lines, ok := glanceTable(v, inner); ok {
		return strings.Join(lines, "\n")
	}
	// Cleaned before it is measured, because the renderer cleans what it draws
	// and a path whose escape sequences were zero cells wide to the measure
	// is longer than that once they are text.
	v = view.MapStrings(v, textclean.Terminal)
	var buf bytes.Buffer
	// Fill: a tile is drawn at the grid's width whether its content wants it
	// or not, so the slack belongs to the content rather than to the space
	// beside it.
	if err := cli.Render(&buf, glanceKeyValue(v, inner), tileRenderOptions(inner)); err != nil {
		return ""
	}
	return buf.String()
}

func tileRenderOptions(inner int) cli.Options {
	return cli.Options{Format: cli.Pretty, Width: inner, Fill: true, Screen: true}
}

// glanceTable draws a table that is too wide to be a grid in the box as a
// borderless one line per row, and says false for every other view and for a
// table that has the room, which the renderer draws as it always did.
//
// Redacted and cleaned first, because the lines are built from the cells and
// not by the renderer: a column the capability marked secret is masked in the
// table the renderer would have drawn, and a cell holding a terminal escape
// sequence — a note's title is what an agent with a grant writes — is made
// harmless there. A reshaping that read the raw cells would be the one place
// neither reached the screen.
//
// Which columns survive when there is not room for all of them is decided from
// the table alone, by rules a reader can predict. The first column is the row's
// name and stays. A column that says nothing — empty in every row, or the same
// in every row of several — goes first: "open" down a list of open notes is not
// a thing to glance at. Then the mostly empty ones, which are an annotation on a
// row and not the row. Then the plain ones, last to first, because the columns
// at the end of a table are the secondary facts. Then the ones that say when (a
// duration, a timestamp), then the ones that grade themselves (status, usage):
// a glance is for a verdict. The rightmost column of prose takes the slack and
// is cut with an ellipsis before anything else is dropped.
func glanceTable(v view.View, inner int) ([]string, bool) {
	t, ok := view.Redact(v).(view.Table)
	if !ok || len(t.Rows) == 0 || len(t.Columns) < 2 || inner < glanceFlexMin*2 {
		return nil, false
	}
	t, ok = view.MapStrings(t, textclean.Terminal).(view.Table)
	if !ok {
		return nil, false
	}
	rows := evenRows(t)
	widths := glanceWidths(t, rows)
	if gridWidth(widths) <= inner {
		return nil, false
	}
	kept, flex, ok := glanceKept(t, rows, widths, inner)
	if !ok {
		return nil, false
	}
	slack := inner - glanceSpan(kept, widths, flex)
	widths[flex] = min(widths[flex], min(widths[flex], glanceFlexMin)+slack)

	lines := []string{glanceLine(t, kept, widths, flex, func(i int) (string, lipgloss.Style) {
		return strings.ToUpper(t.Columns[i].Name), theme.Header
	})}
	for _, row := range rows {
		lines = append(lines, glanceLine(t, kept, widths, flex, func(i int) (string, lipgloss.Style) {
			return row[i], glanceCellStyle(t.Columns[i].Kind, row[i])
		}))
	}
	return append(lines, glanceFooter(t, inner)...), true
}

// evenRows is every row at exactly one cell per column, the way the renderer
// reads them: a short row is padded and a long one has nothing to be named by.
func evenRows(t view.Table) [][]string {
	rows := make([][]string, len(t.Rows))
	for i, r := range t.Rows {
		cells := make([]string, len(t.Columns))
		copy(cells, r)
		rows[i] = cells
	}
	return rows
}

// glanceWidths is each column's natural width: the widest of its heading and
// its cells.
func glanceWidths(t view.Table, rows [][]string) []int {
	out := make([]int, len(t.Columns))
	for i, c := range t.Columns {
		out[i] = lipgloss.Width(strings.ToUpper(c.Name))
		for _, r := range rows {
			out[i] = max(out[i], lipgloss.Width(r[i]))
		}
	}
	return out
}

// gridWidth is what the renderer's bordered grid would need: every column and
// its two cells of padding, and a border beside each and one to close it.
func gridWidth(widths []int) int {
	n := len(widths) + 1
	for _, w := range widths {
		n += w + 2
	}
	return n
}

// glanceKept picks the columns a glanced table keeps and the one that gives,
// or says false when even the name and the squeezed flex column do not fit.
func glanceKept(t view.Table, rows [][]string, widths []int, inner int) (kept []int, flex int, ok bool) {
	kept = []int{0}
	for i := 1; i < len(t.Columns); i++ {
		if !saysNothing(rows, i) {
			kept = append(kept, i)
		}
	}
	flex = 0
	for _, i := range kept[1:] {
		if t.Columns[i].Kind == view.KindText && !sparse(t, rows, i) && widths[i] >= glanceProse {
			flex = i
		}
	}
	var droppable []int
	for _, tier := range []int{dropSparse, dropPlain, dropTimed, dropGraded} {
		var of []int
		for _, i := range kept[1:] {
			if i != flex && dropTier(t, rows, i) == tier {
				of = append(of, i)
			}
		}
		if tier != dropGraded {
			slices.Reverse(of)
		}
		droppable = append(droppable, of...)
	}
	for glanceSpan(kept, widths, flex) > inner && len(droppable) > 0 {
		kept = slices.DeleteFunc(kept, func(i int) bool { return i == droppable[0] })
		droppable = droppable[1:]
	}
	if glanceSpan(kept, widths, flex) > inner {
		return nil, 0, false
	}
	return kept, flex, true
}

// How reluctantly a column is dropped, the least first.
const (
	dropSparse = iota // mostly empty: an annotation on a row, not the row
	dropPlain         // says something of every row, and is neither a time nor a verdict
	dropTimed         // says when
	dropGraded        // grades itself
)

func dropTier(t view.Table, rows [][]string, col int) int {
	switch k := t.Columns[col].Kind; {
	case isGraded(k):
		return dropGraded
	case k == view.KindDuration || k == view.KindTimestamp:
		return dropTimed
	case sparse(t, rows, col):
		return dropSparse
	}
	return dropPlain
}

// sparse reports whether fewer than half the rows have anything in a column that
// does not grade itself: a grant's note, which most grants do not have, is on
// the screen for the one that does and costs the other rows nothing, while the
// text of a notebook is on every row and is what the notebook is for.
func sparse(t view.Table, rows [][]string, col int) bool {
	if isGraded(t.Columns[col].Kind) {
		return false
	}
	full := 0
	for _, r := range rows {
		if r[col] != "" {
			full++
		}
	}
	return 2*full < len(rows)
}

// saysNothing reports whether a column tells a glance nothing: empty in every
// row, or the same in every one of several. A single row is never "the same as
// the others", so a lone grant keeps its columns.
func saysNothing(rows [][]string, col int) bool {
	empty, same := true, true
	for _, r := range rows {
		if r[col] != "" {
			empty = false
		}
		if r[col] != rows[0][col] {
			same = false
		}
	}
	return empty || (same && len(rows) > 1)
}

func isGraded(k view.ColumnKind) bool { return k == view.KindStatus || k == view.KindUsage }

// glanceSpan is the width a set of columns takes with the flex column at the
// narrowest it is squeezed to.
func glanceSpan(kept []int, widths []int, flex int) int {
	n := glanceGap * (len(kept) - 1)
	for _, i := range kept {
		w := widths[i]
		if i == flex {
			w = min(w, glanceFlexMin)
		}
		n += w
	}
	return n
}

// glanceLine is one line of a glanced table: a cell for every kept column,
// padded to the column's width and cut to it where it is the flex column.
func glanceLine(t view.Table, kept []int, widths []int, flex int, cell func(int) (string, lipgloss.Style)) string {
	parts := make([]string, 0, len(kept))
	for _, i := range kept {
		text, style := cell(i)
		if i == flex {
			text = shorten(text, widths[i])
		}
		pad := strings.Repeat(" ", max(widths[i]-lipgloss.Width(text), 0))
		if rightAligned(t.Columns[i].Kind) {
			parts = append(parts, pad+style.Render(text))
		} else {
			parts = append(parts, style.Render(text)+pad)
		}
	}
	return strings.TrimRight(strings.Join(parts, strings.Repeat(" ", glanceGap)), " ")
}

// rightAligned are the kinds the renderer sets flush right, so a number sits
// over its digits whichever layout draws it.
func rightAligned(k view.ColumnKind) bool {
	switch k {
	case view.KindNumber, view.KindBytes, view.KindPercent, view.KindDuration, view.KindUsage:
		return true
	}
	return false
}

// glanceCellStyle colours what the grid colours: a status or a usage cell by
// what it says, and nothing else.
func glanceCellStyle(k view.ColumnKind, cell string) lipgloss.Style {
	switch k {
	case view.KindStatus:
		return theme.StatusStyle(cell)
	case view.KindUsage:
		return theme.UsageStyle(cell)
	}
	return theme.Plain
}

// glanceFooter is what the renderer puts under a table it drew whole, and a
// glanced one owes the same: rows missing, a cursor to the rest, and the
// warnings a partial or flagged answer carries, which are the lines a reshaping
// must never be the reason somebody does not see.
func glanceFooter(t view.Table, inner int) []string {
	var lines []string
	var more []string
	if t.Total > len(t.Rows) {
		more = append(more, fmt.Sprintf("%d of %s", len(t.Rows), format.CountOf(t.Total, "row")))
	}
	if t.Page != nil && t.Page.Next != "" {
		more = append(more, "more rows after "+t.Page.Next)
	}
	if len(more) > 0 {
		lines = append(lines, theme.Subtle.Render(strings.Join(more, " · ")))
	}
	if len(t.Warnings) > 0 {
		var buf bytes.Buffer
		if err := cli.Render(&buf, view.Sections{Warnings: t.Warnings}, tileRenderOptions(inner)); err == nil {
			lines = append(lines, strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")...)
		}
	}
	return lines
}

// shorten cuts s to n cells: a path in the middle, where its end is the part
// that names the thing, and anything else at the end.
func shorten(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if pathLike(s) {
		return middleEllipsis(s, n)
	}
	return ansi.Truncate(s, n, "…")
}

// pathLike is a value with a slash in it and no whitespace: a path or a URL,
// which wraps at no place a reader would choose.
func pathLike(s string) bool {
	return strings.Contains(s, "/") && !strings.ContainsAny(s, " \t\n")
}

// middleEllipsis cuts s to n cells keeping its start and, more of, its end:
// /private/tmp/…/ux/G1/data/rta/kv.age.
func middleEllipsis(s string, n int) string {
	if n < 5 || lipgloss.Width(s) <= n {
		return ansi.Truncate(s, max(n, 0), "…")
	}
	head := max((n-1)/3, 1)
	tail := n - 1 - head
	return ansi.Truncate(s, head, "") + "…" + ansi.TruncateLeft(s, lipgloss.Width(s)-tail, "")
}

// glanceKeyValue shortens, in a key/value tile, the values that are one long
// path: the box would wrap them at an arbitrary cell over several lines, and
// the page that enter opens has the whole of it. Prose is left to wrap, and so
// is a secret, which the renderer masks whatever its length.
func glanceKeyValue(v view.View, inner int) view.View {
	kv, ok := v.(view.KeyValue)
	if !ok {
		return v
	}
	key := 0
	for _, p := range kv.Pairs {
		key = max(key, lipgloss.Width(p.Key))
	}
	room := inner - key - 2
	if room < glanceFlexMin {
		return v
	}
	pairs := slices.Clone(kv.Pairs)
	changed := false
	for i, p := range pairs {
		if pathLike(p.Value) && lipgloss.Width(p.Value) > room {
			pairs[i].Value, changed = middleEllipsis(p.Value, room), true
		}
	}
	if !changed {
		return v
	}
	kv.Pairs = pairs
	return kv
}
