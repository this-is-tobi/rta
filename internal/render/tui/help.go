package tui

import (
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

// The key overlay: `?` on any screen where a bare key is a command lists
// every key that screen answers.
//
// The footer is honest but small. It is two lines, it drops the least
// important hints on a narrow terminal and says so with "…", and it never
// has room for aliases — `q` also answers to ctrl+c on every screen, `esc`
// to the letter that opened a pane, the arrows to hjkl. A k9s or lazygit
// hand reaches for `?` before reading a footer at all. What it finds here is
// generated from the same footerItems the bar packs, so the overlay cannot
// disagree with the bar, and a key added to a screen's bar is listed here
// without anybody remembering to.

// helpOffered says whether `?` opens the overlay on this screen right now:
// where a bare key is a command, and not while a field holds the keyboard —
// a form types `?` into a box, and so do the dashboard's search bar and the
// catalogue's filter while they are being edited. The footer and keyPress
// both ask this, which is what keeps a screen from advertising `?` and
// swallowing it, or answering it unannounced.
func (m Model) helpOffered(screen mode) bool {
	switch screen {
	case modeForm, modeTheme, modePick:
		return false
	case modeDashboard:
		return !m.searchEditing
	case modeBrowse:
		return m.list.FilterState() != list.Filtering
	}
	return true
}

// helpKeys is the overlay's own vocabulary: the key that opened it and esc
// close it, ctrl+c still quits, and nothing else does anything — a key
// pressed while reading what keys do should not act on the screen
// underneath.
func (m Model) helpKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "ctrl+c":
		return m.quit()
	case "esc", "?":
		m.help = false
	}
	return m, nil, true
}

// screenName is how the overlay names the screen under it, in the words
// the docs use.
func screenName(screen mode) string {
	switch screen {
	case modeDashboard:
		return "dashboard"
	case modeBrowse:
		return "catalogue"
	case modeForm:
		return "form"
	case modeRunning:
		return "running screen"
	case modeResult:
		return "result"
	case modePlugins:
		return "plugin inventory"
	case modeProfiles:
		return "profiles"
	case modeProfilePlugins:
		return "profile plugins"
	case modeTheme:
		return "theme editor"
	case modePick:
		return "picker"
	case modeConfirm:
		return "confirmation"
	}
	return "screen"
}

// hiddenKeys are the keys an entry answers to beyond what its display says:
// the aliases the bar has no room for.
func hiddenKeys(it hintItem) []string {
	// Whole names, not substrings: the display "pgup/pgdn" holds the letters u and
	// d, and the page keys u and d went unlisted for it.
	shown := strings.FieldsFunc(it.display, func(r rune) bool { return r == '/' || unicode.IsSpace(r) })
	var out []string
	for _, k := range it.keys {
		// A key that is only whitespace is the space bar under the name a
		// terminal reports it by, and its own entry is "space": listed bare it
		// drew as a gap, "also   x".
		if strings.TrimSpace(k) == "" {
			continue
		}
		if k != it.display && !slices.Contains(shown, k) {
			out = append(out, k)
		}
	}
	return out
}

// wrapWords breaks a run of key names into lines of at most width cells,
// never splitting a name: a half-shown key is the one thing this screen
// exists to avoid.
func wrapWords(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		switch {
		case line == "":
			line = w
		case lipgloss.Width(line)+1+lipgloss.Width(w) <= width:
			line += " " + w
		default:
			lines = append(lines, line)
			line = w
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// helpGutter is the space between the two columns of the overlay.
const helpGutter = 3

// helpEntries are the lines each key the screen answers takes, in the bar's
// order: its row, with the aliases beside it in the muted colour or, when avail
// has no room for that, on lines of their own beneath it. An entry is never
// split, so a key's aliases stay with it wherever the layout breaks the list.
func helpEntries(items []hintItem, keyW, avail int) [][]string {
	entries := make([][]string, 0, len(items))
	for _, it := range items {
		row := theme.Key.Render(it.display) + strings.Repeat(" ", keyW-lipgloss.Width(it.display)+2) + it.label
		entry := []string{row}
		if also := hiddenKeys(it); len(also) > 0 {
			suffix := "also " + strings.Join(also, " ")
			if lipgloss.Width(row)+2+lipgloss.Width(suffix) <= avail {
				entry[0] += "  " + theme.Subtle.Render(suffix)
			} else {
				indent := strings.Repeat(" ", keyW+2)
				for _, chunk := range wrapWords(suffix, avail-len(indent)) {
					entry = append(entry, indent+theme.Subtle.Render(chunk))
				}
			}
		}
		entries = append(entries, entry)
	}
	return entries
}

// helpLines is the entries one after the other.
func helpLines(entries [][]string) []string {
	var lines []string
	for _, e := range entries {
		lines = append(lines, e...)
	}
	return lines
}

// helpColumns lays the keys out in two columns, for a terminal with fewer rows
// than the list has lines. The overlay cannot scroll, so a list taller than the
// screen lost its tail, and the tail is where esc, q and ? are: the keys that
// say how to leave.
//
// The split is the one that leaves the shorter tall column, among those that
// fit the terminal's width, and falls to the narrower of two equally tall ones.
// Aliases wrap inside their column, since half the width is what each has. It
// reports false when no split fits both ways, and the overlay is cut as before.
func (m Model) helpColumns(items []hintItem, keyW int) ([]string, bool) {
	if m.width <= 0 || m.height <= 0 {
		return nil, false
	}
	entries := helpEntries(items, keyW, (m.width-4-helpGutter)/2)
	widest := func(lines []string) int {
		w := 0
		for _, l := range lines {
			w = max(w, lipgloss.Width(l))
		}
		return w
	}
	best, bestRows, bestWidth := 0, 0, 0
	for k := 1; k < len(entries); k++ {
		left, right := helpLines(entries[:k]), helpLines(entries[k:])
		rows, width := max(len(left), len(right)), widest(left)+helpGutter+widest(right)
		if rows+2 > m.height || width+4 > m.width {
			continue
		}
		if best == 0 || rows < bestRows || (rows == bestRows && width < bestWidth) {
			best, bestRows, bestWidth = k, rows, width
		}
	}
	if best == 0 {
		return nil, false
	}
	left, right := helpLines(entries[:best]), helpLines(entries[best:])
	leftW := widest(left)
	lines := make([]string, bestRows)
	for i := range lines {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines[i] = l + strings.Repeat(" ", leftW-lipgloss.Width(l)+helpGutter) + r
	}
	return lines, true
}

// helpView draws the overlay in the panel grammar of every other pane: one
// line per key the screen answers, its aliases beside it in the muted
// colour or, when the terminal is too narrow for that, on their own line
// beneath — a label the bar could not show must not be truncated here too.
// Where the list is taller than the terminal it takes two columns (helpColumns).
func (m Model) helpView() string {
	items := m.footerItems(m.mode)
	keyW := 0
	for _, it := range items {
		keyW = max(keyW, lipgloss.Width(it.display))
	}
	avail := m.width - 4 // panel's own chrome: "│ " and " │"
	if m.width <= 0 {
		avail = 200
	}
	lines := helpLines(helpEntries(items, keyW, avail))
	if m.height > 0 && len(lines)+2 > m.height {
		if columns, ok := m.helpColumns(items, keyW); ok {
			lines = columns
		}
	}
	body := strings.Join(lines, "\n")
	// How to close it is in the border, not in a last line of the body: on a
	// terminal shorter than the list the bottom is what is cut, and an
	// overlay that does not say how to leave is the one that must.
	name := "keys on the " + screenName(m.mode)
	if m.mode == modePick && m.pick != nil {
		name = "keys on the " + m.pick.kind.noun()
	}
	head := panelHead{Title: name, Right: "esc or ? closes"}
	width := max(lipgloss.Width(body)+4, lipgloss.Width(head.Title)+lipgloss.Width(head.Right)+9)
	if m.width > 0 && width > m.width {
		// Too narrow for the screen's name and the way out together: the way
		// out gets shorter, and then the name goes, before the way out does.
		head.Right = "esc closes"
		width = max(lipgloss.Width(body)+4, lipgloss.Width(head.Title)+lipgloss.Width(head.Right)+9)
		if width > m.width {
			head = panelHead{Title: "keys · esc closes"}
		}
		width = min(width, m.width)
	}
	height := 0 // natural, unless the terminal has less
	if m.height > 0 && len(lines)+2 > m.height {
		height = m.height
	}
	box := panel(head, body, width, height, true)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}
