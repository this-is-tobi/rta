package tui

import (
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The one-key actions and view toggles a capability's result offers.
//
// They come from the declaration — plugin.Capability's Actions and Toggles,
// admitted by Validate against the rules this file used to enforce on tables
// of its own (a key every screen owns, bare onto a destructive target, a
// copy key beside Copy). The tables named built-ins only, so a third-party
// list was a table nobody could act on; now a plugin's pg.table.list opens
// pg.table.show on enter exactly as note.list opens note.show, and the shell
// has no opinion about which plugin wrote either. What stays here is the
// shell's own reading of a declaration: which row column seeds which input,
// which keys the dashboard claims for itself (offeredTileActions), what a
// bare action may skip (runAction).

// actionSource says where an action gets the identity of the record it acts
// on — the one thing that differs between acting from a list and acting from
// the page of a single record.
type actionSource int

const (
	srcNone actionSource = iota // no subject: "add" needs nobody's id
	srcRow                      // the selected table row: the columns named for the keys, else its first column
	srcSelf                     // the record the current view is already about
)

func sourceOf(s plugin.ActionSource) actionSource {
	switch s {
	case plugin.ActionRow:
		return srcRow
	case plugin.ActionSelf:
		return srcSelf
	}
	return srcNone
}

// capAction opens a sibling capability from the view you are looking at: a
// button on a dashboard tile, a row action inside a result table, or an
// action on the detail page of one record.
type capAction struct {
	key   string
	label string
	cap   plugin.Capability
	src   actionSource
	// bare runs with what the source gave and asks for nothing more, where
	// the default is to open a form for any input still unfilled. It exists
	// because a declaration is not only for this screen: agent.deny carries
	// `server` and `passphrase` for answering a *remote* queue from the CLI,
	// and stopping the local one-key deny to ask about them would spend the
	// safe answer's whole property on inputs this screen never needs. A
	// required field is still safe under bare — the run refuses without it —
	// so what bare actually waives is the optional-field form, per action
	// and on purpose rather than by a global rule: kv.get's unlock form is
	// the counterexample that keeps this per-action (kv.list's own
	// declaration tells that story).
	bare bool
	// seed is the declaration's input-to-column mapping; see plugin.Action.
	seed map[string]string
}

// viewToggle flips one boolean input of the view you are already looking at.
//
// It is not an action: nothing else runs and nowhere else opens. It is the
// filter on the list in front of you, which is a different thing and needs a
// different mechanism — `note.list` hides checked-off to-dos, so without this
// the re-open action could never find a row to act on. A capability that
// hides part of its own data by default owes the surface a way to ask for
// the rest, and declares it as a plugin.Toggle.
type viewToggle struct {
	key, label, field string
}

// toggles are the current view's declared toggles, in the shell's shape.
func (m Model) toggles() []viewToggle {
	out := make([]viewToggle, 0, len(m.current.Toggles))
	for _, t := range m.current.Toggles {
		out = append(out, viewToggle{key: t.Key, label: t.Label, field: t.Input})
	}
	return out
}

// toggleFor resolves a key to a toggle the current view declares.
func (m Model) toggleFor(key string) (viewToggle, bool) {
	for _, t := range m.toggles() {
		if t.key == key {
			return t, true
		}
	}
	return viewToggle{}, false
}

// capActions resolves a capability's declared actions against the registry.
//
// Validate admits a target only when the same plugin declares it, so a
// target the registry cannot find is a capability the host refused after
// the declaration was read — its action simply does not appear, the way a
// tile for a refused capability does not.
func capActions(reg *registry.Registry, capID string) []capAction {
	c, ok := reg.Capability(capID)
	if !ok {
		return nil
	}
	var out []capAction
	for _, a := range c.Actions {
		target, ok := reg.Capability(a.Target)
		if !ok {
			continue
		}
		out = append(out, capAction{
			key: a.Key, label: a.Label, cap: target,
			src: sourceOf(a.Source), bare: a.Bare, seed: a.Seed,
		})
	}
	return out
}
