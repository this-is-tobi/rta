package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Up and down browse what a box could be completed to, starting from an
// empty box.
//
// bubbles binds up and down to the next and previous suggestion already, and
// they work — once something is typed. It matches suggestions only against
// non-empty text (textinput.updateSuggestions), so an untouched box has no
// matches to cycle, and the arrows are dead exactly where somebody presses
// them first: on a box whose help says what it completes to and shows none
// of it. Tab answers that box by listing the names (tabLists); this is the
// other half, walking them one at a time in the box itself.
//
// The widget owns its text and huh's Input has no way to set it, so a
// candidate goes in the way any text does: as a paste, after the previous
// candidate is deleted with the widget's own ctrl+u. rta remembers what it
// placed, and only while the box still holds exactly that does the next
// arrow move to the neighbour; the moment somebody types, the box is theirs
// and the arrows go back to the widget, which cycles the matches of what
// they typed. A Path box browses the directory listing the same way.
//
// Two forms answer the arrows — the capability form and the theme editor —
// and, as with tab (tabOn), the rule is one free function and the placing is
// one helper, so the two cannot drift.

// browsed is the candidate rta last placed in a box and its index in the
// offer list — what tells "still browsing" from "typed over".
type browsed struct {
	value string
	at    int
}

// browseOn is the rule on the state of one box: what it holds, what rta last
// placed in it, what is on offer, and which way the arrow points. It answers
// with the index to place, or false when the arrow is the widget's to
// handle.
func browseOn(typed string, last browsed, offered []string, down bool) (int, bool) {
	if len(offered) == 0 {
		return 0, false
	}
	if typed == "" {
		if down {
			return 0, true
		}
		return len(offered) - 1, true
	}
	if typed != last.value || last.at >= len(offered) || offered[last.at] != typed {
		return 0, false
	}
	if down {
		return (last.at + 1) % len(offered), true
	}
	return (last.at + len(offered) - 1) % len(offered), true
}

var clearBoxKey = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}

// placeCandidate puts one candidate in the focused box through the form's
// own update: a clear when the box holds the previous candidate, then a
// paste. It returns the model after both and the commands they asked for.
func placeCandidate(m Model, update func(Model, tea.Msg) (tea.Model, tea.Cmd), typed, candidate string) (Model, tea.Cmd) {
	var cmds []tea.Cmd
	if typed != "" {
		next, c := update(m, clearBoxKey)
		m = next.(Model)
		cmds = append(cmds, c)
	}
	next, c := update(m, tea.PasteMsg{Content: candidate})
	m = next.(Model)
	cmds = append(cmds, c)
	return m, tea.Batch(cmds...)
}

func browseFlash(at, of int) string {
	return fmt.Sprintf("%d of %d — ↑↓ browse, type to narrow", at+1, of)
}

// browseOffers is up or down inside a capability form: place the next
// candidate in the focused box, or hand the key to the widget when the box
// is not browsing.
func (m Model) browseOffers(msg tea.KeyPressMsg, down bool) (tea.Model, tea.Cmd) {
	cf := m.form
	name, ok := m.focusedInput()
	if !ok {
		return m.updateForm(msg)
	}
	typed := *cf.bindings[name]
	var offered []string
	if offer := cf.offers[name]; offer != nil {
		offered = offer()
	}
	at, ok := browseOn(typed, cf.browsed[name], offered, down)
	if !ok {
		return m.updateForm(msg)
	}
	nm, cmd := placeCandidate(m, Model.updateForm, typed, offered[at])
	if nm.form != nil {
		nm.form.browsed[name] = browsed{value: offered[at], at: at}
	}
	nm.flash = browseFlash(at, len(offered))
	return nm, cmd
}

// browseInTheme is the same key in the theme editor, over its palette.
func (m Model) browseInTheme(msg tea.KeyPressMsg, down bool) (tea.Model, tea.Cmd) {
	tf := m.themeForm
	name, ok := tf.focusedInput()
	if !ok {
		return m.updateThemeForm(msg)
	}
	typed := *tf.bindings[name]
	offered := tf.offers[name]
	at, ok := browseOn(typed, tf.browsed[name], offered, down)
	if !ok {
		return m.updateThemeForm(msg)
	}
	nm, cmd := placeCandidate(m, Model.updateThemeForm, typed, offered[at])
	if nm.themeForm != nil {
		nm.themeForm.browsed[name] = browsed{value: offered[at], at: at}
	}
	nm.flash = browseFlash(at, len(offered))
	return nm, cmd
}

// browseHint is what a field's help says once it can be browsed as well as
// completed.
func browseHint(desc string) string {
	if strings.Contains(desc, "↑↓") {
		return desc
	}
	return desc + ", ↑↓ browse"
}
