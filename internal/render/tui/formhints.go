package tui

import (
	"slices"

	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// fieldHints is what the box under the cursor can do beyond the keys every
// form shares — the half of a form's footer that moves with the cursor.
//
// huh draws a help line of its own for this, and every form used to show
// both: huh's "tab complete • enter next" inside the frame and rta's "enter
// next · ⇧enter submit · esc cancel" under it. Two bars, two separators, two
// words for the same key, and huh's changing per field while rta's did not.
// keys.go's whole argument is one vocabulary in one place, so huh's help is
// off (WithShowHelp) and this carries the part of it worth keeping: the keys
// a field answers to that nobody could guess from another screen — the
// filter on a picker, the editor on a body, completion on a box that offers
// it. The shared keys stay in the footer's fixed tail, where they always
// were.
//
// completes says whether a free-text box has anything on offer; nil means no
// box does. A form with no field to focus yields nothing.
func fieldHints(f *huh.Form, fields int, completes func(*huh.Input) bool) []hintItem {
	if f == nil || fields == 0 {
		return nil
	}
	pick := func(label string) hintItem {
		return hintItem{display: "↑↓", label: label, rank: rankAction, keys: []string{"up", "down", "k", "j"}}
	}
	switch field := f.GetFocusedField().(type) {
	case *huh.Input:
		if completes != nil && completes(field) {
			// The arrows browse the offer from an empty box (browse.go); k
			// and j type letters here, so only the arrows are advertised.
			return []hintItem{
				action("tab", "complete"),
				{display: "↑↓", label: "browse", rank: rankAction, keys: []string{"up", "down"}},
			}
		}
	case *huh.Select[string]:
		return []hintItem{pick("pick"), action("/", "filter")}
	case *huh.MultiSelect[string]:
		return []hintItem{pick("pick"), action("space", "toggle"), action("/", "filter")}
	case *huh.Confirm:
		return []hintItem{{display: "←→", label: "choose", rank: rankAction,
			keys: []string{"left", "right", "h", "l", "y", "n"}}}
	case *huh.Text:
		// ctrl+e is rta's own binding to $EDITOR (form.go); ctrl+j is huh's
		// newline, and the one worth naming because alt+enter — huh's other
		// newline — is fast submit here and never reaches the field.
		return []hintItem{action("ctrl+e", "$EDITOR"), action("ctrl+j", "new line")}
	}
	return nil
}

// onLastField is whether enter on the field under the cursor completes the form
// instead of moving on. It is huh's own knowledge: a field's Submit binding is
// enabled on the last field and its Next binding on every other
// (charm.land/huh/v2, WithPosition), and the form keeps that current as groups
// show and hide, so asking the binding agrees with what the key will do where
// counting fields would drift from it the moment a conditional box (hideUnless)
// appears or goes.
func onLastField(f *huh.Form) bool {
	if f == nil || f.State != huh.StateNormal {
		return false
	}
	for _, b := range f.GetFocusedField().KeyBinds() {
		if b.Enabled() && b.Help().Desc == "submit" {
			return true
		}
	}
	return false
}

// filterHoldsEsc is whether the picker under the cursor has a filter in force,
// typed into or already applied, which is when esc is the picker's to answer.
//
// huh's own knowledge again: a Select or MultiSelect binds esc to "set filter"
// while its filter box is open and to "clear filter" once a narrowed list is
// left behind, and enables the binding only in that state. Asking the binding
// agrees with the widget where reading its filter could not, since the filter
// text is not exposed.
func filterHoldsEsc(f *huh.Form) bool {
	if f == nil || f.State != huh.StateNormal {
		return false
	}
	field := f.GetFocusedField()
	switch field.(type) {
	case *huh.Select[string], *huh.MultiSelect[string]:
	default:
		return false
	}
	for _, b := range field.KeyBinds() {
		if b.Enabled() && slices.Contains(b.Keys(), "esc") {
			return true
		}
	}
	return false
}

// formEnterWord names enter by what it does where the cursor is: "next" on every
// field but the last, and on the last the thing that completing the form does,
// which afterFormUpdate decides and this reads in the same order.
//
// The footer said "next" on the last field of every form, with the ways to
// submit listed beside it as keys that were not enter — and enter ran the call,
// a write included, or saved a standing grant to an agent. The word has to be
// the one for what the key is about to do, since pressing enter down a form to
// look at its fields is what people do.
func (m Model) formEnterWord() string {
	cf := m.form
	if cf == nil || !onLastField(cf.form) {
		return "next"
	}
	switch {
	case cf.adHocFrom != nil:
		return "hold"
	case cf.configTarget != "", cf.allowTarget != "", cf.credentialEditing, cf.connEditing, cf.profileEditing:
		return "save"
	case !cf.final:
		return "next"
	case m.current.Safety == plugin.Destructive:
		return "preview"
	}
	return "run"
}

// completes reports whether in is one of this form's boxes with something on
// offer — a declared list, a path walk, or values the operator used before.
func (cf *capForm) completes(in *huh.Input) bool {
	for name, widget := range cf.inputs {
		if widget == in {
			return cf.offers[name] != nil
		}
	}
	return false
}
