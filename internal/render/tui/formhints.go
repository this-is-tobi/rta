package tui

import huh "charm.land/huh/v2"

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
