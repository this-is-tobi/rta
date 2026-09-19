package plugin

import (
	"fmt"
	"maps"
	"unicode"
	"unicode/utf8"
)

// What the TUI can do with a result, declared by the capability that returns
// it rather than kept in tables the host owns.
//
// Every behaviour a view has beyond "run it and show it" used to live in
// internal/render/tui, in maps keyed by capability ID: which key opens which
// sibling capability from a row, which key flips a filter, which column `c`
// copies, which views re-run on a timer, whose result may be flashed on a
// footer. Those maps named built-ins only, so a third-party pg.table.list got
// none of it — no enter to a detail page, no row action, no copy — while this
// package's own doc promises there are no second-class plugins. The reference
// shape the project names, the Kubernetes API, puts exactly this kind of
// behaviour on the resource: declared, checked at admission, honoured by
// whichever controller reads it. So the maps became the fields below, and
// Validate is the admission.
//
// None of it is a security boundary. An action runs its target through the
// same path a keypress on the catalogue does — the same form, the same
// confirmation screen for a destructive target, no grant because the TUI is
// the person — and a plugin can only name capabilities of its own.

// ActionSource says what an action acts on: nothing, the selected row of the
// table on screen, or the record the page on screen is about.
type ActionSource string

const (
	// ActionNone is the zero value: the target needs nobody's identity —
	// "add" from a list.
	ActionNone ActionSource = ""
	// ActionRow seeds the target from the selected row: each input from the
	// column of the same name, or the column Seed maps it to, else the row's
	// first column for the positional identity.
	ActionRow ActionSource = "row"
	// ActionSelf seeds the target from the record's own page — the pairs a
	// detail view shows — the same way.
	ActionSelf ActionSource = "self"
)

// Action is one key a result offers: press it and the target opens, seeded
// from what was on screen. A list's row actions, the actions on a record's
// own page, and the buttons on a dashboard tile are all this.
type Action struct {
	// Key is what the person presses: one printable character, or "enter"
	// for a row's own detail page. Validate refuses the keys every screen
	// already owns — ReservedActionKeys — because a row action shadows the
	// screen's own binding of the same key.
	Key string
	// Label is the verb the footer shows beside the key: "remove", "why".
	Label string
	// Target is the capability that opens, one of this plugin's own.
	Target string
	Source ActionSource
	// Bare runs the target with what the source seeded and asks for nothing
	// more, where the default is a form for any input still unfilled. It
	// waives only the optional-field form: the destructive confirmation is
	// reached with or without one, and a required input the source cannot
	// supply is refused at run time — which is why Validate refuses Bare
	// wherever that could happen. It is the one key that acts without a
	// pause, so it is for the fail-safe direction: a deny, a read.
	Bare bool
	// Seed maps an input of the target to the column (or the page's key) it
	// is read from, when that is not the column of the same name: lock.add's
	// `name` is the queue's `agent`. An input seeded from a column the row
	// does not carry is left for the form rather than taken from the first
	// column, which is an id and the wrong value.
	Seed map[string]string
}

// Toggle is a key that flips one Bool input of the capability whose result
// is on screen and runs it again: the filter on the list in front of you.
// Not an action — nothing else opens — and it is what a capability that
// hides part of its own data by default owes the surface, or the action
// that reopens a checked-off note could never find a row to act on.
type Toggle struct {
	Key   string
	Label string
	// Input is the Bool input flipped, one of the capability's own.
	Input string
}

// reservedActionKeys are the keys every TUI screen answers itself, which an
// action or a toggle therefore cannot take: `j` as an action would take
// "down" away from the list it is on. The TUI's own vocabulary is tested to
// be a subset of this set, so a key the shell claims tomorrow is refused to
// plugins the same day. "enter" is listed and is the one exception, allowed
// for a row's own detail page and nothing else. `c` is deliberately absent:
// it is the copy key by convention, and an action may bind it to a copying
// sibling — unless the capability declares Copy for the same key, which
// Validate refuses.
var reservedActionKeys = map[string]string{
	"q": "quit", "esc": "back", "enter": "open", "r": "re-run", "e": "edit inputs", "y": "copy json",
	"b": "browse", ":": "browse", "/": "search", "?": "help", "tab": "next",
	"h": "left", "j": "down", "k": "up", "l": "right",
}

// ReservedActionKeys lists the keys an action may not bind, with what each
// already means on every screen.
func ReservedActionKeys() map[string]string { return maps.Clone(reservedActionKeys) }

// maxLabel bounds an action's or a toggle's verb: it shares a two-line
// footer with every other key the screen answers.
const maxLabel = 24

// checkActions is the admission for what a plugin declares the TUI may do
// with its results. It runs over the whole plugin rather than one capability
// because a target is resolved against the plugin's own list: an action may
// only open a sibling, so no registry is needed to say whether one exists.
//
// Each rule here used to be a test on the host's own tables — consentpane's
// bare rules, the collision check, the copy-key clash — and a table a plugin
// cannot reach is a rule a plugin cannot break or follow. Refused at
// registration, where the author is, the way an unknown field type is.
func (p Plugin) checkActions() error {
	caps := map[string]Capability{}
	for _, c := range p.Capabilities {
		caps[c.ID] = c
	}
	for _, c := range p.Capabilities {
		taken := map[string]bool{}
		for _, a := range c.Actions {
			where := fmt.Sprintf("capability %q: action %q", c.ID, a.Key)
			if err := checkActionKey(c.ID, a.Key, a.Source == ActionRow, taken); err != nil {
				return err
			}
			if a.Label == "" {
				return fmt.Errorf("%s: label is required", where)
			}
			if err := checkLine(where+" label", a.Label, maxLabel); err != nil {
				return err
			}
			target, ok := caps[a.Target]
			if !ok {
				return fmt.Errorf("%s targets %q, which this plugin does not declare; an action opens a sibling capability",
					where, a.Target)
			}
			switch a.Source {
			case ActionNone, ActionRow, ActionSelf:
			default:
				return fmt.Errorf("%s: source %q is not one of row, self or none", where, a.Source)
			}
			// One key, one meaning on one screen: the copy convention and a
			// copying sibling on the same view would be advertised once and
			// answered by whichever the surface checks first.
			if a.Key == "c" && c.Copy != "" {
				return fmt.Errorf("%s binds c, which already copies %q from this view", where, c.Copy)
			}
			if a.Bare {
				// The run path checks Destructive before bare, so the flag
				// would be inert there — which is exactly why it is refused
				// here: a bare destructive action is a claim the code quietly
				// does not honour, waiting for the next refactor of that
				// ordering to become true.
				if target.Safety == Destructive {
					return fmt.Errorf("%s is bare onto destructive %q; the confirmation screen is reached with or without a form",
						where, a.Target)
				}
				// bare waives only the optional-field form; a required input
				// it never asks for turns the one-key action into a validation
				// error. Positional identity comes from the row or the page,
				// so only the non-positional kind — or any kind, from nothing
				// — can strand it.
				for _, f := range target.Inputs {
					if f.Required && (!f.Positional || a.Source == ActionNone) {
						return fmt.Errorf("%s is bare and %q requires %q, which a bare action never asks for",
							where, a.Target, f.Name)
					}
				}
			}
			for input, column := range a.Seed {
				if a.Source == ActionNone {
					return fmt.Errorf("%s seeds %q from a source it does not have; a seed reads the row or the page",
						where, input)
				}
				if _, declared := inputOf(target, input); !declared {
					return fmt.Errorf("%s seeds %q, but %q does not declare input %q", where, input, a.Target, input)
				}
				if column == "" {
					return fmt.Errorf("%s seeds %q from an empty column name", where, input)
				}
			}
		}
		for _, tg := range c.Toggles {
			where := fmt.Sprintf("capability %q: toggle %q", c.ID, tg.Key)
			if err := checkActionKey(c.ID, tg.Key, false, taken); err != nil {
				return err
			}
			if tg.Label == "" {
				return fmt.Errorf("%s: label is required", where)
			}
			if err := checkLine(where+" label", tg.Label, maxLabel); err != nil {
				return err
			}
			f, ok := inputOf(c, tg.Input)
			if !ok {
				return fmt.Errorf("%s flips %q, but %q does not declare input %q", where, tg.Input, c.ID, tg.Input)
			}
			if f.Type != Bool {
				return fmt.Errorf("%s flips %q, which is %s; a toggle flips a Bool", where, tg.Input, f.Type)
			}
		}
		if c.Live && c.Safety != Read {
			return fmt.Errorf("capability %q: Live on a %s capability; a view re-run on a timer must be Read", c.ID, c.Safety)
		}
		if c.Flash && c.Safety == Read {
			return fmt.Errorf("capability %q: Flash on a Read capability, whose result is always a page", c.ID)
		}
		if c.Copy != "" {
			if err := checkLine(fmt.Sprintf("capability %q: copy", c.ID), c.Copy, maxLabel); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkActionKey admits one key: a single printable character nobody's
// screen owns, or enter for a row's own page, and not one this capability
// has already bound.
func checkActionKey(capID, key string, detailPage bool, taken map[string]bool) error {
	where := fmt.Sprintf("capability %q", capID)
	if key == "enter" {
		if !detailPage {
			return fmt.Errorf("%s: enter opens a row's own page and nothing else; an action bound to it reads the row", where)
		}
	} else {
		r, size := utf8.DecodeRuneInString(key)
		if size == 0 || size != len(key) || !unicode.IsGraphic(r) || unicode.IsSpace(r) {
			return fmt.Errorf("%s: key %q; want one printable character, or enter for a row's page", where, key)
		}
		if why, reserved := reservedActionKeys[key]; reserved {
			return fmt.Errorf("%s binds %q, which every screen already uses for %s", where, key, why)
		}
	}
	if taken[key] {
		return fmt.Errorf("%s binds %q twice", where, key)
	}
	taken[key] = true
	return nil
}

func inputOf(c Capability, name string) (Field, bool) {
	for _, f := range c.Inputs {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}
