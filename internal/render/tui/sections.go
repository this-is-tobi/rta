package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/plugin"
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

// SectionNames are the sections an arrangement can draw, in any environment: the
// names its entries state, and the `<plugin> · <profile>` an entry that follows the
// switch or names a profile is given when that profile holds several connections to
// the plugin. Exported for `rta config check` and `rta doctor`, which say that a
// `sections:` entry names no tile, and cannot say it for a group named after an
// environment that is not the one switched on by looking at what is drawn now:
// the entry is right the day that environment is, and wrong only when no
// environment could ever make it a name.
func SectionNames(reg *registry.Registry, dash config.Dashboard, cfg config.Config) map[string]bool {
	shown := dash
	shown.Hidden = nil
	names := map[string]bool{}
	for _, t := range buildTilesWith(reg, shown, nil)[1:] {
		if t.section != "" {
			names[t.section] = true
			continue
		}
		if config.RefInstance(t.profile) != "" {
			continue
		}
		ns := plugin.Namespace(t.cap.ID)
		for name, p := range cfg.Profiles {
			if (t.profile == "" || config.RefName(t.profile) == name) && p.Trusted() && len(p.Instances(ns)) >= 2 {
				names[config.AutoSectionName(ns, name)] = true
			}
		}
	}
	return names
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

// headerAt is the section whose header is drawn on the terminal line y, for the
// click that folds it.
func (m Model) headerAt(y int) (string, bool) {
	if row, ok := m.gridRowAt(y); ok && row.isHeader() {
		return row.header.name, true
	}
	return "", false
}

// foldVerb is what folding the section would be called now: the word on the key
// that does it, and in the answer it gives.
func (m Model) foldVerb(name string) string {
	if m.collapsed(name) {
		return "unfold"
	}
	return "fold"
}

// sectionKeys answers the keys that fold: enter and space on a header, which is
// all a header can be asked to do, and z and Z wherever the dashboard has sections.
// Not handled — false — it leaves the key to the dashboard's own switch, and when
// there is no section at all that is every key, so a dashboard without sections
// has no key it did not have, and a tile's own `z` action is still reachable.
func (m Model) sectionKeys(key string) (Model, bool) {
	switch {
	case m.selected < 0 && m.header != "" && (key == "enter" || key == " " || key == "space"):
		m.setCollapsed(m.header, !m.collapsed(m.header))
		m.clampScroll()
		return m, true
	case key == "z" && m.hasSections():
		m.flash = m.foldSelected()
		return m, true
	case key == "Z" && m.hasSections():
		m.flash = m.foldAll()
		return m, true
	}
	return m, false
}

// foldSelected folds the section of the selected header or tile, or unfolds it,
// and says what it did when it could not. A tile that is in none is told so: the
// key is on the bar whenever the dashboard has a section, and an answer is better
// than a press that goes nowhere.
func (m *Model) foldSelected() string {
	name := ""
	switch {
	case m.selected < 0:
		name = m.header
	case m.selected > 0 && m.selected < len(m.tiles):
		name = m.tiles[m.selected].section
	}
	if name == "" {
		m.guide("this tile is in no section — Z folds every one")
		return m.flash
	}
	m.setCollapsed(name, !m.collapsed(name))
	// Folding the section a tile is in leaves the selection on the header it is
	// now under, which is the nearest thing there is of it.
	m.keepSelection()
	m.clampScroll()
	return ""
}

// foldAll folds every section, or unfolds them all when none is open: one key
// that can be pressed again to undo itself, which a fold that always folded
// could not be.
func (m *Model) foldAll() string {
	runs := m.sectionRuns()
	fold := slices.ContainsFunc(runs, func(r sectionRun) bool { return !m.collapsed(r.name) })
	for _, r := range runs {
		m.setCollapsed(r.name, fold)
	}
	m.keepSelection()
	m.clampScroll()
	verb := "unfolded"
	if fold {
		verb = "folded"
	}
	return verb + " " + format.Count(len(runs), "section", "sections")
}

// keepSelection puts the selection back on something that is drawn, for the
// moments the tiles were built again under it: the selected tile went, or is now
// in a section that starts folded, or the section whose header held it is gone.
// A tile that is folded away is not drawn, so the header of its section is the
// place that is left of it.
//
// **A header that is gone leaves the selection on the first stop of what is
// drawn, not on the search bar.** The keys that bring about this very case — V
// to the next screen, the switch to another environment's — are pressed by
// somebody working in the grid, and the first stop is the nearest thing in it to
// where they were; the search bar is only left for a screen with nothing on it.
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

// packRows lays the tiles [lo, hi) out on rows of config.GridTracks: a tile goes
// on the row it fits, and a row is closed when the next one would not.
func (m Model) packRows(lo, hi int) []gridRow {
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
		w := m.span(i)
		if used+w > config.GridTracks {
			flush()
		}
		cur = append(cur, i)
		used += w
		if used >= config.GridTracks {
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
	name := sectionLabel(run.name)
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

// sectionLabel is a section's name as the screen says it, on the header and in a
// flash: as it is when it reads as itself, and quoted otherwise. A name is a
// heading a file states, so it can be one that is not — a line break in it made
// the header two rows where the layout counts one, and a flash that names it a
// line cut in two — and textclean.Name, which is plugin.ListedName, is how every
// listing of names a stranger chose shows one, with the escape sequence or the
// reordering character in it written out where textclean.Terminal dropped it
// without a word.
func sectionLabel(name string) string { return textclean.Name(name) }

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
		sum.problem = theme.Grade(theme.StatusBad).Render(fmt.Sprintf("● %d bad", counts[gradeBad]))
	case counts[gradeWarn] > 0:
		sum.problem = theme.Grade(theme.StatusWarn).Render(fmt.Sprintf("● %d warn", counts[gradeWarn]))
	}
	switch {
	case loading > 0:
		sum.rest = theme.Subtle.Render(fmt.Sprintf("%d loading", loading))
	case counts[gradeBad]+counts[gradeWarn] == 0 && counts[gradeGood] > 0:
		sum.rest = theme.Grade(theme.StatusGood).Render("ok")
	}
	return sum
}
