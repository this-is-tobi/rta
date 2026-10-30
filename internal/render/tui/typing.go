package tui

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// The search takes the keyboard when it is asked for.
//
// The dashboard opens with the search line holding the selection, and a letter
// pressed there is a command like it is on every tile: `p` the plugin inventory,
// `t` the theme, `q` quits. The search starts on `/` from wherever the selection
// is, and on `enter` when the line itself is selected; from then on the letters
// are the query, the rule the catalogue's filter box follows too.
//
// It used to be the other way round: while the line held the selection, the
// first printable key started the query. A line drawn with a caret does look
// like a box, but making it answer like one meant the keys of the dashboard
// depended on where the selection happened to rest — `q` quit from every tile
// but the first, `V` and `U` needed carve-outs so they could still be pressed
// on the screen that opens with every tile failing for want of the store, and
// the footer on that line advertised keys it did not answer. One rule for the
// whole screen is cheaper than a list of exceptions to the line's rule.

// maxPastedQuery is as much of a paste as the bar takes: a capability ID or a
// phrase from a note is well inside it, and a pasted file is not a search.
const maxPastedQuery = 120

// pasteIntoSearch is a paste onto the dashboard, wherever the selection is: the
// text becomes the query, exactly as if it had been typed after /, and the
// search takes the selection as / gives it. A box that takes `kv.get` typed
// and drops it pasted is the one place a person copying an ID from a message
// would not look for the reason, and the terminal sends a paste as one event
// that no key handler sees, so a paste can never be mistaken for a run of
// commands. Nothing else on the dashboard takes text, so there is no tile a
// paste could have been meant for instead.
//
// Line breaks and runs of space collapse to one space and anything that does
// not print is dropped, so a paste cannot put a control sequence in a query
// that is drawn back on screen.
func (m Model) pasteIntoSearch(msg tea.PasteMsg) (Model, bool) {
	if m.mode != modeDashboard || m.help {
		return m, false
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) || unicode.IsSpace(r) {
			return r
		}
		return -1
	}, msg.Content)
	text := strings.Join(strings.Fields(clean), " ")
	if r := []rune(text); len(r) > maxPastedQuery {
		text = string(r[:maxPastedQuery])
	}
	if text == "" {
		return m, true
	}
	m.selected, m.searchEditing, m.searchSel = 0, true, 0
	m.query += text
	m.clampScroll()
	return m, true
}
