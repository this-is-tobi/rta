package tui

import (
	"strings"

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
	case modeForm, modeTheme, modeCopyPick:
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
	case modeCopyPick:
		return "copy picker"
	case modeConfirm:
		return "confirmation"
	}
	return "screen"
}

// hiddenKeys are the keys an entry answers to beyond what its display says:
// the aliases the bar has no room for.
func hiddenKeys(it hintItem) []string {
	var out []string
	for _, k := range it.keys {
		if k != it.display && !strings.Contains(it.display, k) {
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

// helpView draws the overlay in the panel grammar of every other pane: one
// line per key the screen answers, its aliases beside it in the muted
// colour or, when the terminal is too narrow for that, on their own line
// beneath — a label the bar could not show must not be truncated here too.
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
	lines := make([]string, 0, len(items)+2)
	for _, it := range items {
		row := theme.Key.Render(it.display) + strings.Repeat(" ", keyW-lipgloss.Width(it.display)+2) + it.label
		if also := hiddenKeys(it); len(also) > 0 {
			suffix := "also " + strings.Join(also, " ")
			if lipgloss.Width(row)+2+lipgloss.Width(suffix) <= avail {
				row += "  " + theme.Subtle.Render(suffix)
			} else {
				lines = append(lines, row)
				indent := strings.Repeat(" ", keyW+2)
				for _, chunk := range wrapWords(suffix, avail-len(indent)) {
					lines = append(lines, indent+theme.Subtle.Render(chunk))
				}
				continue
			}
		}
		lines = append(lines, row)
	}
	lines = append(lines, "", theme.Subtle.Render("esc or ? closes this"))
	body := strings.Join(lines, "\n")
	width := lipgloss.Width(body) + 4
	if m.width > 0 {
		width = min(width, m.width)
	}
	box := panel(panelHead{Title: "keys on the " + screenName(m.mode)}, body, width, 0, true)
	if m.width > 0 && m.height > 0 {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}
	return box
}
