package cli

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// What a table looks like when there is no room for a table.
//
// A grid trades vertical space for scanability: every row on one line, every
// column under its heading, and the eye reads down a column to compare. That
// trade only works while each column is wide enough to hold something. Below
// that it inverts — an audit finding at sixty cells read
//
//	│ lodash   │ fail   │ lodash     │ A03:2025   │ https://os │
//	│          │        │ 4.17.20    │ Software   │ v.dev/vuln │
//	│          │        │ (npm) —    │ Supply     │ erability/ │
//
// which is every value present and none of them legible, with the borders
// spending a sixth of each line saying where the gutters are. An operator on a
// narrow terminal, or a split pane, or a phone over ssh, was being shown the
// data in the one arrangement that hides it.
//
// Records give the whole width to one field at a time. Nothing is dropped,
// nothing is truncated, and the prose wraps through the same wrap() every
// other value in this renderer goes through — so the hyphen rule holds and a
// package name or a flag does not break in half.
//
// It is a *rendering* choice and only ever applies where a width was measured
// from a real terminal. A pipe has no width, so scripts, goldens and diffs are
// untouched: this cannot change what a redirected `rta` writes.

// recordStyle carries the per-column facts the record layout still needs.
type recordStyle struct {
	// highlight is the 1-based row to accent, or 0. The TUI's selected row.
	highlight int
	// status names the columns whose values are graded, so they keep their
	// colour — the one piece of a grid's meaning that is not positional.
	status map[int]bool
}

// minRecordKey keeps the label gutter from collapsing when every column name
// is short, so the values still line up with each other.
const minRecordKey = 6

// fitsAsGrid reports whether a table has the width to be drawn as one.
//
// The test is per column and it is about the *text* columns, because they are
// the ones that need room: a status is six cells wide and will never be
// anything else, and giving it a share of a squeeze is how the prose beside it
// ends up at ten. So the fixed columns are charged their natural width and the
// rest is divided among the text ones, which must each clear minWrap — the
// same floor wrap() already refuses to break below, for the same reason.
//
// A table with no width to measure against (a pipe) always fits: unconstrained
// output is the grid it has always been.
func fitsAsGrid(t view.Table, headers []string, st styles) bool {
	if st.width <= 0 || len(t.Columns) == 0 {
		return true
	}
	natural := naturalWidths(t, headers)
	// One border cell per column plus one to close the table.
	budget := st.width - (len(t.Columns) + 1)
	var text []int
	for i, c := range t.Columns {
		if c.Kind == view.KindText {
			text = append(text, i)
			continue
		}
		budget -= natural[i]
	}
	if len(text) == 0 {
		// Nothing here wraps, so there is no prose to protect. Either it fits
		// or the shrink loop deals with it, which is what it has always done.
		return true
	}
	if budget <= 0 {
		return false
	}
	return narrowestSqueezed(natural, text, budget) >= minGridColumn
}

// minGridColumn is the narrowest a squeezed text column may end up before the
// grid stops being worth drawing.
//
// Four cells above minWrap rather than equal to it, and the gap is deliberate.
// minWrap is the point below which wrap() gives up entirely; a column at
// exactly that width still *renders*, it just renders one short word per line.
// This is the point below which the arrangement is worse than the alternative,
// which arrives first.
const minGridColumn = minWrap + 4

// narrowestSqueezed reports the width the tightest text column ends up with
// once the budget is shared out, or a width above the floor when no text
// column has to give anything up.
//
// Water-filling rather than a plain average, because an average is exactly the
// wrong measure here: one wide prose column beside a narrow one averages
// comfortably while the narrow one is at four cells. A column whose natural
// width is already inside its share keeps it and hands the remainder back to
// the columns that are actually being squeezed, which is what a shrink does.
func narrowestSqueezed(natural []int, text []int, budget int) int {
	pending := append([]int(nil), text...)
	remaining := budget
	for len(pending) > 0 {
		share := remaining / len(pending)
		var squeezed []int
		settled := false
		for _, i := range pending {
			if natural[i] <= share {
				remaining -= natural[i]
				settled = true
				continue
			}
			squeezed = append(squeezed, i)
		}
		if !settled {
			return remaining / len(squeezed)
		}
		pending = squeezed
	}
	// Every text column fits at its natural width, so nothing is squeezed and
	// there is no floor to clear.
	return minGridColumn
}

// naturalWidths is each column measured the way it will be rendered: the
// widest cell in it, plus the two cells of padding lipgloss adds.
func naturalWidths(t view.Table, headers []string) []int {
	out := make([]int, len(t.Columns))
	for i := range t.Columns {
		w := 0
		if i < len(headers) {
			w = lipgloss.Width(headers[i])
		}
		for _, row := range t.Rows {
			if i < len(row) {
				w = max(w, lipgloss.Width(row[i]))
			}
		}
		out[i] = w + 2
	}
	return out
}

// prettyRecords draws one block per row: the first column as the block's name,
// then every other column as a labelled line beneath it.
//
// The first column is the name because that is what tables in this tool put
// there — a check, a capability, a key, an entry — and a record needs a title
// more than it needs its title repeated as a field.
func prettyRecords(w io.Writer, t view.Table, headers []string, rows [][]string,
	st styles, rs recordStyle) error {

	// The label gutter, sized to the widest label that is actually used — and
	// capped, because a heading is data too. A thirty-three-cell column name
	// pushed every value line past a fifty-cell terminal by five, in the layout
	// whose entire job is to fit one. A quarter of the width leaves three
	// quarters for the value, which is the right way round when the label is
	// the part the reader already knows.
	keyW := minRecordKey
	for i := 1; i < len(headers); i++ {
		if usedColumn(rows, i) {
			keyW = max(keyW, lipgloss.Width(headers[i]))
		}
	}
	if st.width > 0 {
		keyW = min(keyW, max(minRecordKey, st.width/4))
	}
	// Labels are lower case here. A grid's headings are shouted because they
	// are a rule across the top; a label beside its own value is read as part
	// of the sentence, and SHOUTING every third word of it is noise.
	label := func(i int) string {
		if i >= len(headers) {
			return ""
		}
		return strings.ToLower(headers[i])
	}

	for n, row := range rows {
		if n > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		name := ""
		if len(row) > 0 {
			name = row[0]
		}
		if name == "" {
			name = fmt.Sprintf("(%d)", n+1)
		}
		// A name that does not say what it is gets told.
		//
		// Most first columns are the record's identity in words — a check, an
		// entry, a capability — and repeating "check" above every one of them is
		// noise. A *kinded* first column is not that: `todo list` leads with a
		// number, and a bare "2" heading a block reads as a stray figure where
		// "ID │ 2" under a heading never could.
		if len(t.Columns) > 0 && t.Columns[0].Kind != view.KindText {
			name = strings.TrimSpace(label(0)) + " " + strings.TrimSpace(name)
		}
		// The selected record is marked as well as accented: a background band
		// on a heading is easy to miss on a terminal with a busy theme, and the
		// marker survives --no-color, where the accent is nothing at all.
		marker, style := "  ", st.header
		if rs.highlight > 0 && n == rs.highlight-1 {
			marker = "▸ "
			if st.color {
				style = style.Background(theme.Faint)
			}
		}
		// Wrapped before it is styled, and wrapped at all: a record's name is a
		// value like any other and can be longer than the terminal. Styling
		// first would hand the wrapper a string whose width it has to unpick,
		// and would stretch a highlight band across the padding of every line.
		nameIndent := hanging("    ", st.width)
		for i, line := range strings.Split(wrap(marker+name, st.width, nameIndent), "\n") {
			if i > 0 {
				line = nameIndent + style.Render(strings.TrimPrefix(line, nameIndent))
			} else {
				// A single token longer than the budget is broken by wrap()
				// after the marker, which left the first line holding nothing
				// but the marker and the name hanging underneath a blank
				// heading. Dropped rather than printed: an empty title line is
				// not a record, it is a gap in the middle of one.
				line = strings.TrimPrefix(line, marker)
				if strings.TrimSpace(line) == "" {
					continue
				}
				line = marker + style.Render(line)
			}
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
		for i := 1; i < len(row); i++ {
			if strings.TrimSpace(row[i]) == "" {
				continue // an empty field is not a fact worth a line
			}
			value := row[i]
			if st.color && rs.status[i] {
				value = theme.StatusStyle(value).Render(value)
			}
			// Four cells of indent: two for the marker gutter, two so the
			// fields sit visibly inside their heading.
			//
			// The *value* is wrapped, not the whole line — the same shape
			// prettyKeyValue uses, and for the reason its first line is full
			// width and this one's was not. wrap() charges the continuation
			// indent against the budget it gives every line, so handing it the
			// label as part of the string made the first line short by the
			// width of the gutter and every line after it correct.
			gutter := strings.Repeat(" ", 4+keyW+2)
			// wrap() floors its budget at minWrap and returns the string
			// untouched below it, so on a genuinely tiny terminal a value put
			// beside its label simply overflowed. Its own line has the whole
			// width, which is the most there is to give.
			own := hanging("      ", st.width)
			// A label wider than the capped gutter takes the line to itself
			// rather than being cut down. pad() does not truncate, so leaving
			// it beside its value both broke the alignment the gutter exists
			// for and pushed that one line past the edge — and a label is the
			// part of a record a reader uses to find their way, so shortening
			// it is the wrong economy.
			if st.width > 0 &&
				(lipgloss.Width(label(i)) > keyW || st.width-lipgloss.Width(gutter) < minWrap) {
				if _, err := fmt.Fprintln(w, labelLine(label(i), st)); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(w, own+wrap(value, st.width, own)); err != nil {
					return err
				}
				continue
			}
			// A value that cannot be broken and cannot fit beside its label
			// takes the line to itself, where it has the whole width.
			//
			// The case is a URL, and it is the case this layout most has to get
			// right: an advisory link split across two lines is a link nobody
			// can copy, which is the defect that started all of this. Giving up
			// the label gutter buys back nine or ten cells, and for the one
			// shape that cannot spend them any other way that is often the
			// difference between one line and two.
			if unbreakable(value) && lipgloss.Width(value) > st.width-lipgloss.Width(gutter) {
				if _, err := fmt.Fprintln(w, labelLine(label(i), st)); err != nil {
					return err
				}
				if _, err := fmt.Fprintln(w, own+wrap(value, st.width, own)); err != nil {
					return err
				}
				continue
			}
			line := "    " + st.key.Render(pad(label(i), keyW)) + "  " + wrap(value, st.width, gutter)
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	if t.Total > len(t.Rows) {
		_, err := fmt.Fprintln(w, st.muted.Render(fmt.Sprintf("%d of %d rows", len(t.Rows), t.Total)))
		return err
	}
	return nil
}

// labelLine renders a heading on a line of its own.
//
// **Wrapped, because it reaches this path precisely because it did not fit.**
// Both callers above give a label the whole line when it is wider than the
// capped gutter — and then emitted it raw, so a heading wider than the
// terminal itself ran straight past the right edge of the layout whose entire
// job is to fit one. The condition is `4 + width(heading) > width`: any
// terminal under twenty columns for rta's own widest built-in heading, and
// wider than that for a plugin-declared one, which is a string this renderer
// does not control.
//
// Broken rather than shortened, for the reason the callers already give about
// not cutting a label down: it is what a reader uses to find their way down a
// record, and an elided heading costs more than an extra line. Hard-broken
// below wrap()'s floor as well — that floor exists to stop a text column being
// squeezed against a wide key gutter, and a label on its own line has no
// gutter to be squeezed by.
func labelLine(label string, st styles) string {
	const indent = "    "
	budget := st.width - len(indent)
	if st.width <= 0 || budget < 1 || lipgloss.Width(label) <= budget {
		return indent + st.key.Render(label)
	}
	lines := hardBreakOverlong(ansi.Wordwrap(shieldHyphens(label), budget, ""), budget)
	for i, line := range lines {
		lines[i] = indent + st.key.Render(restoreHyphens(strings.TrimRight(line, " ")))
	}
	return strings.Join(lines, "\n")
}

// hanging picks the continuation indent for a value at this width.
//
// wrap() floors its budget at minWrap — below that every break lands mid-word
// and it declines to break at all — so an indent wider than width-minWrap
// makes every continuation line overflow by the difference, on exactly the
// narrow terminals this layout exists for. Where there is not room for both
// the indent and a readable line, the indent goes.
func hanging(indent string, width int) string {
	if width > 0 && width-lipgloss.Width(indent) < minWrap {
		return ""
	}
	return indent
}

// unbreakable reports whether a value is one token with nowhere to wrap.
//
// Whitespace is the only break wrap() will take: hyphens are shielded (every
// hyphen in this tool is inside an identifier somebody may be about to paste)
// and nothing else is a break point. So a value with no space in it either
// fits on a line or gets cut mid-token, and there is no third outcome to hope
// for.
func unbreakable(s string) bool { return !strings.ContainsAny(s, " \t") }

// usedColumn reports whether any row has something to say in this column. A
// column that is empty for every row would otherwise widen the label gutter
// for a label that never appears.
func usedColumn(rows [][]string, i int) bool {
	for _, row := range rows {
		if i < len(row) && strings.TrimSpace(row[i]) != "" {
			return true
		}
	}
	return false
}
