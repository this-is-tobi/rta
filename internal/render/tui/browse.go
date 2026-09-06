package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The capability catalogue.
//
// Dozens of capabilities in one flat list is a phone book: everything is
// there and nothing is findable. Two things fix it, and neither is a feature
// so much as an admission of what the list is *for*.
//
// Grouping: capabilities belong to plugins, and the plugin is the unit people
// think in ("what can net do?"). IDs already sort into those groups; what was
// missing was saying so on screen.
//
// Permissions: the safety class decides whether an AI agent can call
// something at all, and it was rendered as a glyph glued to the ID (`kv.rm ⚠`)
// which reads as decoration. It is now its own right-hand column, in the same
// vocabulary the rest of the system uses, with the grant requirement beside it
// — so "what could an agent do here?" is answered by looking down one column.

// pluginHeader is a section title inside the capability list. It is a real
// list item because bubbles lists are flat: headers rendered from inside a
// delegate would break the fixed-height scrolling math.
type pluginHeader struct {
	p plugin.Plugin
}

// FilterValue is empty on purpose: while filtering, section headers are noise
// — the answer is the matches, and a header for a group of none is worse than
// no header at all. An empty value drops it from filtered results.
func (h pluginHeader) FilterValue() string { return "" }

// catalogueItems builds the list: one header per plugin, then its
// capabilities. Capabilities arrive sorted by ID, which is already grouped by
// namespace.
func catalogueItems(reg *registry.Registry) []list.Item {
	byNS := map[string][]plugin.Capability{}
	for _, c := range reg.Capabilities() {
		ns := grant.Namespace(c.ID)
		byNS[ns] = append(byNS[ns], c)
	}
	items := make([]list.Item, 0, len(reg.Capabilities())+len(reg.Plugins()))
	for _, p := range reg.Plugins() { // already sorted by name
		items = append(items, pluginHeader{p: p})
		for _, c := range byNS[p.Name] {
			items = append(items, capItem{c: c})
		}
	}
	return items
}

// capDelegate renders the catalogue as a table: one line per capability,
// columns that line up down the whole list, and a band per plugin.
//
// It used to be two lines per entry plus a blank one — ID and permission on
// the first, summary indented under it — which is a card, not a catalogue.
// Sixty-seven capabilities at three rows each is two hundred rows of
// scrolling to read a list whose whole purpose is answering "what can this
// thing do", and eleven of them visible at a time on a forty-row terminal.
// One row each shows thirty-four, and the columns are what make that
// readable rather than merely dense: the eye goes down a column to compare
// permissions instead of tracking a right-aligned value across ragged rows.
//
// It stays a bubbles list rather than becoming a real table widget, and that
// is the deliberate part. Filtering, cursor movement and scrolling are the
// list's, and they work on a flat slice of items; a table widget would mean
// reimplementing all three to gain nothing the columns below do not already
// give. What was asked for was a table to read, not a table type.
type capDelegate struct {
	// idW is the ID column width, measured once from the whole catalogue so
	// the columns do not shift as the list scrolls or filters.
	idW int
	// permW is the permission column width.
	permW int
}

func newCapDelegate(items []list.Item) capDelegate {
	// Seeded from the headings, because a column narrower than its own
	// heading is a column whose heading is a lie: `CAPABILI…` over a
	// catalogue of eight-cell ids, and `PERM…` whenever nothing installed
	// happens to be destructive. The data widens it from there.
	d := capDelegate{
		idW:   lipgloss.Width(headID),
		permW: lipgloss.Width(headPermission),
	}
	for _, it := range items {
		c, ok := it.(capItem)
		if !ok {
			continue
		}
		d.idW = max(d.idW, lipgloss.Width(c.c.ID))
		d.permW = max(d.permW, lipgloss.Width(permissionText(c.c)))
	}
	return d
}

func (capDelegate) Height() int                         { return 1 }
func (capDelegate) Spacing() int                        { return 0 }
func (capDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

// columns resolves the three column widths for the available width. The ID
// and permission columns get what they measured; the summary takes the rest
// and is the only one that gives ground, because a truncated summary still
// reads and a truncated ID is not something you can type.
func (d capDelegate) columns(width int) (id, perm, summary int) {
	id, perm = d.idW, d.permW
	summary = width - marker - id - gutter - perm - gutter
	if summary < minSummary {
		// Out of room: the permission column goes first. It is the one whose
		// values are a closed set of five words somebody learns once.
		perm = 0
		summary = width - marker - id - gutter
	}
	return id, perm, max(summary, 0)
}

const (
	marker     = 4  // "  ❯ " / "    "
	gutter     = 2  // between columns
	minSummary = 24 // below this a summary stops being a sentence

	// The headings, named once so the column widths and the header line
	// cannot disagree about how much room a heading needs.
	headID         = "CAPABILITY"
	headPermission = "PERMISSION"
	headSummary    = "SUMMARY"
)

func (d capDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	width := max(m.Width()-2, 20)
	selected := index == m.Index()

	switch it := item.(type) {
	case pluginHeader:
		// A band rather than a heading: at one line per row the group needs
		// to be visible at a glance, not merely present.
		label := " " + strings.ToUpper(it.p.Name) + " "
		count := fmt.Sprintf(" %d ", len(it.p.Capabilities))
		rule := max(width-lipgloss.Width(label)-lipgloss.Width(count)-2, 0)
		fmt.Fprint(w, theme.Subtle.Render("─")+theme.Title.Render(label)+
			theme.Subtle.Render(strings.Repeat("─", rule)+count+"─"))
	case capItem:
		idW, permW, sumW := d.columns(width)
		mark, id := "    ", theme.Key.Render(cell(it.c.ID, idW))
		if selected {
			mark, id = theme.AccentTxt.Render("  ❯ "), theme.Title.Render(cell(it.c.ID, idW))
		}
		line := mark + id
		if permW > 0 {
			line += strings.Repeat(" ", gutter) + permissionCell(it.c, permW)
		}
		if sumW > 0 {
			line += strings.Repeat(" ", gutter) +
				theme.Subtle.Render(ansi.Truncate(it.c.Summary, sumW, "…"))
		}
		fmt.Fprint(w, line)
	}
}

// browseHeader is the column header, drawn above the list so it does not
// scroll away. A table whose headings leave the screen on the first page is
// a table you have to remember the shape of.
func (m Model) browseHeader(width int) string {
	// One expression for the content width, shared with the row renderer
	// (capDelegate.Render): the count used to be right-aligned against the
	// full width while the columns resolved against two cells less, so it
	// floated past the edge of the table it counts.
	inner := max(width-2, 20)
	idW, permW, sumW := m.cols.columns(inner)
	line := strings.Repeat(" ", marker) + cell(headID, idW)
	if permW > 0 {
		line += strings.Repeat(" ", gutter) + cell(headPermission, permW)
	}
	if sumW > 0 {
		// Unpadded: the count sits on the right of this same line, and a
		// SUMMARY heading stretched to the full column would push it off.
		line += strings.Repeat(" ", gutter) + headSummary
	}
	// The count rides on the same line rather than in a status bar of its
	// own: it is one number, and a line spent on one number is a row of the
	// catalogue nobody gets to see.
	return rightAlign(theme.Subtle.Render(line), theme.Subtle.Render(m.catalogueCount()), inner)
}

// catalogueCount says how much of the catalogue is in front of you, which
// during a filter is the only honest way to read a short list: "3 of 78" is
// a filter working, an unqualified "3" is a catalogue that lost something.
func (m Model) catalogueCount() string {
	total := 0
	for _, it := range m.list.Items() {
		if _, isCap := it.(capItem); isCap {
			total++
		}
	}
	if m.list.FilterState() != list.Unfiltered {
		shown := 0
		for _, it := range m.list.VisibleItems() {
			if _, isCap := it.(capItem); isCap {
				shown++
			}
		}
		return fmt.Sprintf("%d of %d ", shown, total)
	}
	// Where you are, not just how much there is.
	//
	// The list holds eighty-five rows and shows twenty-seven, and it advances
	// a page at a time rather than a row at a time — so pressing down at the
	// bottom replaces the whole screen, including the row the cursor was on.
	// With the list's own pagination switched off there was nothing anywhere
	// saying a second page existed, which is what makes a catalogue that
	// moves perfectly well read as one that is stuck.
	if pages := m.list.Paginator.TotalPages; pages > 1 {
		return fmt.Sprintf("%d capabilities · page %d/%d ",
			total, m.list.Paginator.Page+1, pages)
	}
	return fmt.Sprintf("%d capabilities ", total)
}

// browseView frames the catalogue: column headings, the list, and the same
// hint bar every other screen uses. The list's own help line is switched off
// in New — it speaks a different vocabulary ("↑/k up", "/ filter") from the
// rest of the app, which is exactly the kind of seam a person has to stop
// and read rather than recognise.
func (m Model) browseView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	return m.browseHeader(width) + "\n" + m.list.View() + "\n" + m.browseFooter()
}

// browseFooter is the catalogue's hint bar, in the app's own vocabulary.
func (m Model) browseFooter() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	m.width = width
	return m.footerFor(modeBrowse)
}

// cell left-aligns s in a column of w rendered cells.
func cell(s string, w int) string {
	if gap := w - lipgloss.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return ansi.Truncate(s, w, "…")
}

// permissionText is the permission column's value, unstyled — what the
// column is measured against.
func permissionText(c plugin.Capability) string {
	s := string(c.Safety)
	if c.HumanOnly {
		return s + " · not for agents"
	}
	// The unprofiled picture: what an agent needs to reach this against the
	// operator's base connection. Naming a profile always needs a grant on top,
	// which the profile picker says at the point of choosing one rather than
	// here, where it would read as a property of the capability.
	if grant.Required(c, "") {
		// The second half of the answer: a person has to allow this one at
		// the time, and it stops being allowed on its own.
		s += " · grant"
	}
	return s
}

// permissionLabel is the styled permission value: what this capability is
// allowed to do, and what an agent needs before it may. The search tile
// shows it right-aligned; the catalogue pads it into a column.
func permissionLabel(c plugin.Capability) string {
	style := theme.Subtle
	switch c.Safety {
	case plugin.Write:
		style = theme.WarnText
	case plugin.Destructive:
		style = theme.BadText
	}
	return style.Render(permissionText(c))
}

// permissionCell is permissionLabel padded to a column width, with the
// padding left unstyled so the column still lines up.
func permissionCell(c plugin.Capability, w int) string {
	text := permissionText(c)
	if gap := w - lipgloss.Width(text); gap > 0 {
		return permissionLabel(c) + strings.Repeat(" ", gap)
	}
	return permissionLabel(c)
}

// rightAlign pads left against right within width, measuring rendered cells so
// styling does not shift the column.
func rightAlign(left, right string, width int) string {
	gap := width - ansi.StringWidth(left) - ansi.StringWidth(right)
	if gap < 1 {
		return ansi.Truncate(left, max(width-ansi.StringWidth(right)-1, 1), "…") + " " + right
	}
	return left + strings.Repeat(" ", gap) + right
}

// onCapability reports whether the cursor is on something enter can open.
//
// It asks that rather than "is this a header" because there is a third
// state, and it is the one that bit: a filter narrows the list under the
// cursor, the index is left past the end of what survived, and SelectedItem
// returns nothing at all. A check written as "not a header" is satisfied by
// nothing, so the cursor was left pointing at no row and enter had nothing
// to open — filter for a capability, press enter, and the catalogue simply
// did not respond. It predates the table rewrite.
func (m Model) onCapability() bool {
	_, ok := m.list.SelectedItem().(capItem)
	return ok
}

// settleCursor puts the cursor back on a row enter can act on: a section
// header is a label rather than a destination, and an index left past the
// end of a freshly filtered list is not a row at all.
//
// The clamp and the predicate above guard different things — landing on a
// header while walking the list, and pointing past the end of one that just
// got shorter — and for the filter case either would be enough on its own.
// Both are kept because only one of them is about filtering: remove the
// clamp and a filtered list still works by accident, through a check that
// was never written with it in mind.
//
// It carries on in the direction of travel, then reverses if that runs out —
// the first item in the list *is* a header, so pressing up at the top has
// nowhere further to go and must come back down rather than stick.
func (m *Model) settleCursor(down bool) {
	// Clamp first. Walking from an out-of-range index would step through the
	// whole list without ever landing anywhere.
	if n := len(m.list.VisibleItems()); n > 0 && m.list.Index() >= n {
		m.list.Select(0)
	}
	step := func(forward bool) {
		if forward {
			m.list.CursorDown()
			return
		}
		m.list.CursorUp()
	}
	for _, dir := range []bool{down, !down} {
		for range len(m.list.Items()) {
			if m.onCapability() {
				return
			}
			before := m.list.Index()
			step(dir)
			if m.list.Index() == before {
				break // boundary: try the other way
			}
		}
	}
}
