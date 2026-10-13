package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
)

// Sections: tiles drawn together under a header that folds.
//
// **The tiles are grouped in the slice, and the layout reads the groups off it.**
// Everything that moves, hides, selects or addresses a tile does it by its
// position in m.tiles — `[` and `]` swap runs of it, H cuts one out, a click is
// turned into an index, `order:` is the order it is in. Grouping is therefore
// done where the tiles are built (groupSections), so that a section is a
// contiguous run of the one list that all of that already works on, and the
// layout draws a header where a run begins. A layout that regrouped for the
// picture alone would have drawn the sections and left every key handler
// reasoning about a different order.
//
// **A header is a row of the grid and a stop of the selection**, not decoration
// above it. It has to be a row because the scroll window, the row budget and the
// click that lands on it are all counted in rows (tileRows); and a stop because
// a folded section has no tile to select, so the only way to unfold it from the
// keyboard is for the header itself to be somewhere the arrows go.
//
// **The fold is the session's, and it is not written.** `z` is a way to look at
// the screen — hide the five panels while you read the sixth — and V, which
// changes what the screen shows, is session-only for the same reason, where H
// and `[` change what the arrangement is and are written. The file says how a
// section starts (`sections: <name>: collapsed:`); the session may fold away from
// that and a switch of view or a restart is back at it. Persisting it would take
// a place that is not the arrangement: the config is the operator's text, written
// under a lock and rewritten with its comments kept, and a toggle on every
// keypress there is a diff in somebody's dotfiles repository for each time they
// glanced at a pane — so it would have to be a state file beside the config, per
// view and section, read at start and written on a fold, with the rule for a
// section that was since renamed or deleted. That is a feature of its own, and
// until somebody asks for the screen to remember, the config's `collapsed:` is
// the way to say what you want it to start as.

// sectionHeaderHeight is the lines a header takes, folded or not: one, so that
// folding a section gives back everything but the line that says what is in it.
const sectionHeaderHeight = 1

// sectionRun is one section as it sits in m.tiles: the tiles [lo, hi) are all in
// it, and are only those.
type sectionRun struct {
	name   string
	lo, hi int
}

// size is how many tiles the section holds.
func (r sectionRun) size() int { return r.hi - r.lo }

// sectionRuns are the sections in the order they are drawn, which is the order of
// their first tile. Contiguous by construction (groupSections), so a section is
// the longest run of one name; the same name coming back after another tile would
// be two runs and two headers, which is why every way of changing the order of
// m.tiles keeps the runs whole.
func (m Model) sectionRuns() []sectionRun {
	var runs []sectionRun
	for i := 1; i < len(m.tiles); i++ {
		name := m.tiles[i].section
		if name == "" {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1].name == name && runs[n-1].hi == i {
			runs[n-1].hi++
			continue
		}
		runs = append(runs, sectionRun{name: name, lo: i, hi: i + 1})
	}
	return runs
}

// hasSections reports whether any tile is in a section. A dashboard that has none
// is drawn exactly as it was before sections existed: no header, no fold key in
// the footer, nothing to select that is not a tile.
func (m Model) hasSections() bool {
	return slices.ContainsFunc(m.tiles, func(t tile) bool { return t.section != "" })
}

// groupSections puts the tiles of a section together at the place of the first of
// them, in the order they had. A tile in no section keeps its place, so a
// dashboard that names none comes back as it went in.
func groupSections(tiles []tile) []tile {
	if !slices.ContainsFunc(tiles, func(t tile) bool { return t.section != "" }) {
		return tiles
	}
	out := make([]tile, 0, len(tiles))
	placed := map[string]bool{}
	for i, t := range tiles {
		switch {
		case t.section == "":
			out = append(out, t)
		case placed[t.section]:
		default:
			placed[t.section] = true
			for _, u := range tiles[i:] {
				if u.section == t.section {
					out = append(out, u)
				}
			}
		}
	}
	return out
}

// gridRow is one line of the grid: a section's header, or the tiles that share a
// row of it. Which tile sits on which row, and where a header is, is decided here
// and nowhere else.
type gridRow struct {
	// header is the section this row is the header of, the zero run for a row of tiles.
	header sectionRun
	// cells are the tile indexes of a row of tiles.
	cells []int
}

func (r gridRow) isHeader() bool { return r.header.name != "" }

// foldKey names a section on a screen: the view it is drawn on and its name, since
// `pg · staging` on the default screen and on a view of its own are two sections
// that were folded separately.
type foldKey struct{ view, name string }

// fold is what the session did to a section. against is the state the file gave it
// when it did, so a fold stays only for as long as the file still says what it said
// then: edit `collapsed:` in another terminal and the section follows the file
// again, which is what the edit was for, without a hook in the code that adopts an
// edited block.
type fold struct{ collapsed, against bool }

// collapsed reports whether a section is folded to its header now: what the
// session did to it, or how the file starts it.
func (m Model) collapsed(name string) bool {
	start := m.dash.SectionOf(name).Collapsed
	if f, ok := m.folds[foldKey{m.view, name}]; ok && f.against == start {
		return f.collapsed
	}
	return start
}

// setCollapsed folds or unfolds a section for the session. A new map each time: a
// Model is copied on every message, and a map shared by the copy that was is a
// fold nobody asked for in the one that is.
func (m *Model) setCollapsed(name string, collapsed bool) {
	folds := make(map[foldKey]fold, len(m.folds)+1)
	maps.Copy(folds, m.folds)
	folds[foldKey{m.view, name}] = fold{collapsed: collapsed, against: m.dash.SectionOf(name).Collapsed}
	m.folds = folds
}

// onHeader is whether the header row of the section called name holds the selection.
func (m Model) onHeader(name string) bool { return m.selected < 0 && m.header == name }

// keepSelection puts the selection back on something that is drawn, for the
// moments the tiles were built again under it: the selected tile went, or is now
// in a section that starts folded, or the section whose header held it is gone.
// A tile that is folded away is not drawn, so the header of its section is the
// place that is left of it.
//
// **A header that is gone leaves the selection on the first stop of what is
// drawn, not on the search bar.** The search bar takes every letter typed as a
// query, and the keys that bring about this very case — V to the next screen, the
// switch to another environment's — are letters: from the search bar the second V
// was typed into the query, and the screen that was meant to cycle stayed where
// it was. The first stop is a header or a tile, where a letter is a command, and
// it is where the selection starts on a screen that has just been drawn; the
// search bar is only left for a screen with nothing on it.
func (m *Model) keepSelection() {
	if m.selected < 0 {
		if !slices.ContainsFunc(m.sectionRuns(), func(r sectionRun) bool { return r.name == m.header }) {
			m.selected, m.header = 0, ""
			if stops := m.stops(); len(stops) > 0 {
				m.selectStop(stops[0])
				m.goalCol = stops[0].col
			}
		}
		return
	}
	m.selected = min(m.selected, len(m.tiles)-1)
	if m.selected > 0 {
		if name := m.tiles[m.selected].section; name != "" && m.collapsed(name) {
			m.selectStop(stop{header: name})
		}
	}
}

// gridAt is the geometry of a grid wanting cols columns, 0 meaning as many as fit.
func (m Model) gridAt(want int) grid {
	// The dashboard was two columns at every width, so a 200-cell terminal
	// drew two 100-cell tiles of a six-line summary — half the screen spent
	// on whitespace, with the tiles that did not fit pushed below the fold.
	cols := max(min(m.width/minColWidth, maxCols), 1)
	if want > 0 {
		cols = max(want, 1)
	}
	// A configured column count still has to fit: honouring columns: 4 on a
	// 60-cell terminal draws four 14-cell tiles, which is not what anybody
	// meant by it.
	cols = max(min(cols, m.width/minColWidth), 1)
	// One gap cell between columns; hit-testing divides by tileW+1.
	return grid{cols: cols, tileW: (m.width - (cols - 1)) / cols}
}

// gridFor is the grid tile i is laid out on: its section's own column count when
// the section states one, the dashboard's otherwise.
func (m Model) gridFor(i int) grid {
	if i > 0 && i < len(m.tiles) {
		if name := m.tiles[i].section; name != "" {
			if n := m.dash.SectionOf(name).Columns; n > 0 {
				return m.gridAt(n)
			}
		}
	}
	return m.grid()
}

// packRows lays the tiles [lo, hi) out on rows of grid g: a wide tile takes a row
// to itself, the rest fill it.
func (m Model) packRows(lo, hi int, g grid) []gridRow {
	var rows []gridRow
	var cur []int
	used := 0
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, gridRow{cells: cur})
			cur, used = nil, 0
		}
	}
	for i := lo; i < hi; i++ {
		w := m.spanOn(i, g)
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

// renderSectionHeader draws one section's header: where it is folded and what it
// is called on the left, and on the right how many tiles it holds and, for the
// worst of them, what they say — so folding a section hides the panels and never
// the fact that one of them is red.
//
// **What a narrow header gives up is the count, then the end of the name, and the
// status never.** The status is the one thing a fold must not hide, and a header
// is narrowest where a person has the least room to notice it, so it is not a
// part that can be left off to fit: the count goes first, since the panels under
// an open header say it by being there; and when the whole name no longer fits
// beside the status, the name is cut with an ellipsis, because the status is a
// few cells and the name is what has the room to give. A name is cut no shorter
// than minNameCells while a shorter form of the status would leave it more.
// Dropping the status before the name was how a section of red tiles read as
// quiet at 60 columns.
//
// The selection is a `❯` where the search line has one, because a header has no
// border to colour and a terminal that shows no colour still has to show where
// the selection is.
func (m Model) renderSectionHeader(run sectionRun, selected bool) string {
	width := max(m.width, 1)
	glyph := "▾"
	if m.collapsed(run.name) {
		glyph = "▸"
	}
	mark, title := "  ", theme.PanelTitle
	if selected {
		mark, title = "❯ ", theme.Key
	}
	lead := mark + glyph + " "
	name := textclean.Terminal(run.name)
	sum := m.sectionSummary(run)
	// What is left of a line for the name beside this much of the right-hand
	// side: the gap and the two cells of rule between them are four.
	room := func(right string) int { return width - lipgloss.Width(lead) - lipgloss.Width(right) - 4 }

	right := ""
	forms := sum.forms()
	whole := slices.IndexFunc(forms, func(f string) bool { return room(f) >= lipgloss.Width(name) })
	switch {
	case whole >= 0:
		right = forms[whole]
	case sum.problem != "" || sum.rest != "":
		for _, f := range sum.statusForms() {
			right = f
			if room(f) >= min(lipgloss.Width(name), minNameCells) {
				break
			}
		}
		name = ansi.Truncate(name, max(room(right), 1), "…")
	default:
		name = ansi.Truncate(name, max(width-lipgloss.Width(lead), 1), "…")
	}
	left := lead + title.Render(name)
	if right == "" {
		return ansi.Truncate(left, width, "…")
	}
	fill := width - lipgloss.Width(left) - lipgloss.Width(right) - 2
	// Still one line on a terminal too narrow for the mark, an ellipsis and the
	// status together: a header that wrapped would be two rows where the layout
	// counts one.
	return ansi.Truncate(left+" "+theme.Border.Render(strings.Repeat("─", max(fill, 1)))+" "+right, width, "")
}

// minNameCells is the least of its name a header keeps for itself while a shorter
// form of what it says on the right would leave it more.
const minNameCells = 12

// headerSummary is what a header says of the tiles in its section, in the parts it
// gives up one at a time when the line is short.
type headerSummary struct {
	// count is how many tiles there are.
	count string
	// problem is the worst of what the tiles say, "" when none says anything is wrong.
	problem string
	// rest is that some tiles have not answered yet, or that all have and it is fine.
	rest string
}

// forms are the right-hand side from the fullest to the briefest: the count, the
// problem and the rest; the problem and the rest; the problem alone. A section
// whose tiles say nothing has only its count.
func (s headerSummary) forms() []string {
	return slices.Concat([]string{joinSummary(s.count, s.problem, s.rest)}, s.statusForms())
}

// statusForms are the forms that say what the tiles say, and not how many there are.
func (s headerSummary) statusForms() []string {
	var forms []string
	for _, f := range []string{joinSummary(s.problem, s.rest), s.problem} {
		if f != "" && !slices.Contains(forms, f) {
			forms = append(forms, f)
		}
	}
	return forms
}

func joinSummary(parts ...string) string {
	parts = slices.DeleteFunc(slices.Clone(parts), func(p string) bool { return p == "" })
	return strings.Join(parts, theme.Subtle.Render(" · "))
}

// sectionSummary is what a header says of its section: the tile count, then the
// worst of what the tiles say. Nothing is said about a section whose tiles grade
// nothing but that it is still waiting for some of them to answer, and "ok" only
// when every tile has answered and at least one said so, since a header that called
// its section fine on the strength of tiles that said nothing would be asserting
// what no tile did.
func (m Model) sectionSummary(run sectionRun) headerSummary {
	var counts [gradeBad + 1]int
	loading := 0
	for i := run.lo; i < run.hi; i++ {
		g, answered := tileGrade(m.asReturned(i))
		if !answered {
			loading++
			continue
		}
		counts[g]++
	}
	sum := headerSummary{count: theme.Subtle.Render(format.Count(run.size(), "tile", "tiles"))}
	switch {
	case counts[gradeBad] > 0:
		sum.problem = theme.BadText.Render(fmt.Sprintf("● %d bad", counts[gradeBad]))
	case counts[gradeWarn] > 0:
		sum.problem = theme.WarnText.Render(fmt.Sprintf("● %d warn", counts[gradeWarn]))
	}
	switch {
	case loading > 0:
		sum.rest = theme.Subtle.Render(fmt.Sprintf("%d loading", loading))
	case counts[gradeBad]+counts[gradeWarn] == 0 && counts[gradeGood] > 0:
		sum.rest = theme.GoodText.Render("ok")
	}
	return sum
}
