package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Typing on the landing screen finds things.
//
// The dashboard opens with the search bar holding the selection, drawn with
// the same caret a text box has, and a person who types a word into a box that
// looks like a box does not expect `p` to open the plugin inventory, `t` the
// theme editor and the rest of the word to be eaten as commands in whatever
// pane that landed in: typing `password` opened the inventory, `trust` opened
// the theme form with "rust" already in its colour box, and `dns` did nothing
// at all. The single-letter commands belong to a tile, and a tile is not what
// is selected yet.
//
// So while the bar holds the selection a printable key is a letter of the
// query, the rule the catalogue's filter box already follows ("q, / and + are
// letters of the query"). The first letter starts the search and is the first
// character in it. Moving to a tile with an arrow hands the letters back to
// the commands, and the footer changes with the selection so it only ever
// advertises what the keys do right now.
//
// The rule needs a tile to hand the letters back to. A dashboard with every
// tile hidden has nothing under the bar to move to, so there the bar would
// swallow `p`, the key its own note says brings the hidden ones back, and the
// plugin inventory, the profiles and the theme would be reachable by nobody:
// with no tile the letters stay commands.
//
// What stays a command, because none of them can be a letter of a capability
// search: `/` and enter focus the bar, `+` opens the catalogue to add a tile
// from, `:` opens it to browse, `?` lists the keys, and ctrl+c quits from
// anywhere. `q` is a letter here, which is the price: quitting from a cold
// start is ctrl+c, or an arrow to a tile and then `q`.

// searchIdleItems is the bar while the search bar holds the selection and not
// yet the keyboard: what a key does right now, and only that. The pane keys
// are not in it as keys, since `p` would type a p; they are in it as the
// thing to do next, so the inventory, the profiles and the theme stay
// findable from the landing screen.
func searchIdleItems() []hintItem {
	return []hintItem{
		{display: "type", label: "to search", rank: rankPrimary, typed: true},
		{
			display: bindSelect.display, label: "select", rank: rankPrimary,
			keys: []string{"up", "down", "left", "right", "tab"},
		},
		alias(labelled(bindOpen, "search"), "/"),
		{display: ":", label: "browse", rank: rankExtra, keys: []string{":"}},
		item(bindAdd),
		{display: "p f t", label: "plugins, profiles, theme on a tile", rank: rankExtra},
		{display: "ctrl+c", label: "quit", rank: rankLeave, keys: []string{"ctrl+c"}},
	}
}

// lettersAreQuery is whether a printable key is a letter of a query right now:
// the bar holds the selection and there is a tile to hand the letters back to.
func (m Model) lettersAreQuery() bool { return m.selected == 0 && len(m.tiles) > 1 }

// typedIntoSearch reports the text of a key press that should start a search:
// the search bar holds the selection, the key prints something, and that
// something is not one of the few keys the bar keeps for itself.
func (m Model) typedIntoSearch(msg tea.KeyPressMsg) (string, bool) {
	if !m.lettersAreQuery() || msg.Text == "" {
		return "", false
	}
	if msg.Mod&(tea.ModCtrl|tea.ModAlt|tea.ModMeta|tea.ModHyper|tea.ModSuper) != 0 {
		return "", false
	}
	switch msg.Text {
	case "/", "+", ":", "?":
		return "", false
	}
	for _, r := range msg.Text {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	// A space opens the bar and is not typed into it: a query that begins with
	// one only has to be trimmed again, and the caret already says it is open.
	if msg.Text == " " && m.query == "" {
		return "", true
	}
	return msg.Text, true
}
