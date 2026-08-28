package tui

import (
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The one-key actions and view toggles a capability's result offers, and
// the tables that declare them per capability.

// actionSource says where an action gets the identity of the record it acts
// on — the one thing that differs between acting from a list and acting from
// the page of a single record.
type actionSource int

const (
	srcNone actionSource = iota // no subject: "add" needs nobody's id
	srcRow                      // the selected table row (its first column)
	srcSelf                     // the record the current view is already about
)

// capAction opens a sibling capability from the view you are looking at: a
// button on a dashboard tile, a row action inside a result table, or an
// action on the detail page of one record.
type capAction struct {
	key   string
	label string
	cap   plugin.Capability
	src   actionSource
}

// capActionSpecs declares which capabilities each view can reach in one key.
// One table drives every surface: dashboard tile buttons (minus "enter",
// which opens the tile), row actions inside result tables, and the actions
// on a record's own page — so a note is as editable as a task, wherever you
// happen to be looking at it. Keys must not collide with navigation
// (hjkl/arrows, tab, b, :, /, q) or result keys (r, y, c) — "c" here means
// this table's own row-action copy (kv.list/kv.show → kv.copy); it is also
// the key copyvalue.go's copySpecs uses for a capability with no sibling
// action to copy through, checked both on a result already open
// (resultView) and directly against a tile's own preview (dashFooter,
// tui.go's modeDashboard "c" case). A capability must not appear in both
// tables, or whichever one this loop or that case reaches first shadows
// the other's hint silently — today that would require a capability that
// both backs a tile (its own "overview") and declares a capActionSpecs "c"
// entry for itself, which none currently does.
var capActionSpecs = map[string][]struct {
	key, label, id string
	src            actionSource
}{
	"todo.list": {
		{"enter", "show", "todo.show", srcRow},
		{"a", "add", "todo.add", srcNone},
		{"e", "edit", "todo.edit", srcRow},
		{"d", "done", "todo.done", srcRow},
		// The undo for `d`, one key away from it: marking the wrong task
		// complete is a one-keystroke mistake and should cost one keystroke
		// to take back.
		{"o", "re-open", "todo.reopen", srcRow},
		{"x", "remove", "todo.rm", srcRow},
	},
	"note.list": {
		{"enter", "show", "note.show", srcRow},
		{"a", "add", "note.add", srcNone},
		{"e", "edit", "note.edit", srcRow},
		{"x", "remove", "note.rm", srcRow},
	},
	// The hosts file is a list you manage, not just read: park an override
	// with `t`, drop it with `x`. `t` rather than `d` — d is "done" on the
	// task lists, and a key that means two things across two screens is a
	// key you hesitate over.
	"net.hosts.list": {
		{"a", "add", "net.hosts.add", srcNone},
		{"t", "toggle", "net.hosts.toggle", srcRow},
		{"x", "remove", "net.hosts.rm", srcRow},
	},
	// A finding names a package, and the question it raises second is what
	// pulled that package in — which decides whether the fix is a version bump
	// in a file you own or somebody else's release. `w` rather than a letter
	// already spoken for, and the same word every package manager uses for it.
	//
	// The form it opens is seeded with the path the listing ran against, so the
	// answer is about the project on screen rather than the working directory.
	"audit.deps": {
		{"w", "why", "audit.why", srcRow},
	},
	// A stale grant is something you notice on the dashboard, so taking it
	// back has to be possible from there and not only from a shell. `e`
	// re-issues the grant under the cursor, which is what "it expired while I
	// was still using it" actually needs.
	"grant.list": {
		{"a", "allow", "grant.allow", srcNone},
		{"e", "renew", "grant.allow", srcRow},
		{"x", "revoke", "grant.revoke", srcRow},
	},
	// Deliberately no "reveal" action: a secret shown because a key was
	// pressed on a list is a secret shown by accident. `kv get` asks for it
	// by name, which is the point at which you meant to. Enter opens the
	// entry's metadata instead, which is everything about it that is safe to
	// put on a screen.
	//
	// `c` is not that decision quietly reversed. A copy puts nothing on the
	// screen, so it leaves none of what a reveal leaves — no scrollback, no
	// screen share, no photograph of somebody's monitor, no asciinema
	// recording — and the next thing anybody copies undoes it, while a
	// revealed secret stays revealed for as long as the buffer lives. It is
	// also not one keystroke: the passphrase and identity are inputs like any
	// other, so every kv action opens the unlock form on the way.
	//
	// kv.edit is absent for the opposite reason to the missing reveal: it
	// hands the terminal to $EDITOR, and the terminal is what this program
	// is drawing on.
	"kv.list": {
		{"enter", "show", "kv.show", srcRow},
		{"c", "copy", "kv.copy", srcRow},
		{"a", "add", "kv.set", srcNone},
		{"e", "set", "kv.set", srcRow},
		{"m", "rename", "kv.rename", srcRow},
		{"x", "remove", "kv.rm", srcRow},
	},
	// The kv tile is `kv status`, which is about the store rather than any
	// entry — so its actions are the two things you want from there: the
	// list, and a new secret.
	"kv.status": {
		{"s", "secrets", "kv.list", srcNone},
		{"a", "add", "kv.set", srcNone},
	},
	"kv.show": {
		{"c", "copy", "kv.copy", srcSelf},
		{"e", "set", "kv.set", srcSelf},
		{"m", "rename", "kv.rename", srcSelf},
		{"x", "remove", "kv.rm", srcSelf},
		{"a", "add", "kv.set", srcNone},
	},
	// The detail pages act on the record they are already showing.
	"todo.show": {
		{"e", "edit", "todo.edit", srcSelf},
		{"d", "done", "todo.done", srcSelf},
		{"o", "re-open", "todo.reopen", srcSelf},
		{"x", "remove", "todo.rm", srcSelf},
		{"a", "add", "todo.add", srcNone},
	},
	"note.show": {
		{"e", "edit", "note.edit", srcSelf},
		{"x", "remove", "note.rm", srcSelf},
		{"a", "add", "note.add", srcNone},
	},
}

// viewToggle flips one boolean input of the view you are already looking at.
//
// It is not an action: nothing else runs and nowhere else opens. It is the
// filter on the list in front of you, which is a different thing and needs a
// different mechanism — `todo.list` hides completed tasks, so without this
// the re-open action could never find a row to act on. A capability that
// hides part of its own data by default owes the surface a way to ask for
// the rest.
type viewToggle struct {
	key, label, field string
}

var viewToggleSpecs = map[string][]viewToggle{
	"todo.list": {{key: "A", label: "show done", field: "all"}},
	"kv.list":   {{key: "D", label: "detail", field: "detail"}},
}

// toggleFor resolves a key to a toggle declared for this capability.
func toggleFor(capID, key string) (viewToggle, bool) {
	for _, t := range viewToggleSpecs[capID] {
		if t.key == key {
			return t, true
		}
	}
	return viewToggle{}, false
}

// capActions resolves the declared actions for a capability against the
// registry. Unknown IDs simply do not appear.
func capActions(reg *registry.Registry, capID string) []capAction {
	var out []capAction
	for _, spec := range capActionSpecs[capID] {
		if c, ok := reg.Capability(spec.id); ok {
			out = append(out, capAction{key: spec.key, label: spec.label, cap: c, src: spec.src})
		}
	}
	return out
}
