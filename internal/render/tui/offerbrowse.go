package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/boxlist"
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
// A list box is browsed one item at a time. The item walked is the last one,
// and everything ahead of it is the head the walk began from: "a, " offers the
// rest of the candidates after it, and the walk replaces the item after the
// head, never the head. The offer is therefore asked for the head (offersAt)
// and not for the box, which once a candidate is placed reads as a box whose
// last item is that candidate and offers only what begins with it.
//
// Two forms answer the arrows — the capability form and the theme editor —
// and, as with tab (tabOn), the rule is one free function and the placing is
// one helper, so the two cannot drift.

// browsed is the candidate rta last placed in a box and its index in the
// offer list — what tells "still browsing" from "typed over". For a list box
// base is the head the candidate was written after, which the walk keeps
// asking the offer about until the box is typed in.
type browsed struct {
	value string
	at    int
	base  string
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
	base, ok := cf.browseBase(name, typed)
	if !ok {
		return m.updateForm(msg)
	}
	var offered []string
	if offerAt := cf.offersAt[name]; offerAt != nil {
		offered = itemsAfter(base, offerAt(base))
	} else if offer := cf.offers[name]; offer != nil {
		offered = offer()
	}
	at, ok := browseOn(strings.TrimPrefix(typed, base), cf.browsed[name], offered, down)
	if !ok {
		return m.updateForm(msg)
	}
	nm, cmd := placeCandidate(m, Model.updateForm, typed, base+offered[at])
	if nm.form != nil {
		nm.form.browsed[name] = browsed{value: offered[at], at: at, base: base}
	}
	nm.flash = browseFlash(at, len(offered))
	return nm, cmd
}

// browseBase is the head a walk of this box is over, or false when the box is
// the widget's to handle. A box that is not a list is walked whole, so its head
// is empty. A list box is mid-walk while it still holds what was placed, and
// the walk keeps its head; otherwise a walk starts only where an item does — an
// empty box, or one that ends at a comma and the space typed after it — since
// text in the middle of an item is somebody narrowing it, and the widget cycles
// what matches that.
func (cf *capForm) browseBase(name, typed string) (string, bool) {
	if cf.offersAt[name] == nil {
		return "", true
	}
	if last := cf.browsed[name]; last.value != "" && typed == last.base+last.value {
		return last.base, true
	}
	if head, _, _ := boxlist.Last(typed); head == typed {
		return typed, true
	}
	return "", false
}

// itemsAfter is what each text adds to head, for the texts that begin with it:
// the items a walk over a list box steps through.
func itemsAfter(head string, texts []string) []string {
	items := make([]string, 0, len(texts))
	for _, text := range texts {
		if item, ok := strings.CutPrefix(text, head); ok {
			items = append(items, item)
		}
	}
	return items
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
