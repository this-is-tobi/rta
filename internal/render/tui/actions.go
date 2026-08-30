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
// (hjkl/arrows, tab, b, :, /, q), result keys (r, y, c), or "e" — "c" here
// means this table's own row-action copy (kv.list/kv.show → kv.copy); it is
// also the key copyvalue.go's copySpecs uses for a capability with no
// sibling action to copy through, checked both on a result already open
// (resultView) and directly against a tile's own preview (dashFooter,
// tui.go's modeDashboard "c" case). "e" is resultKeys' own generic "edit
// inputs" (dispatch.go) — reopen the form this result ran with, seeded —
// and resultKeys checks this table first, so an entry declaring "e" for
// itself would silently make edit-inputs unreachable for that capability
// rather than share the key: this table's own loop returns before the
// generic case is ever reached. A capability must not appear in both
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
		{"u", "update", "todo.edit", srcRow},
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
		{"u", "update", "note.edit", srcRow},
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
	// back has to be possible from there and not only from a shell. `n`
	// re-issues the grant under the cursor, which is what "it expired while I
	// was still using it" actually needs.
	"grant.list": {
		{"a", "allow", "grant.allow", srcNone},
		{"n", "renew", "grant.allow", srcRow},
		{"x", "revoke", "grant.revoke", srcRow},
	},
	// The consent queue, answerable from the screen the operator is already
	// looking at: a parked call is a question with exactly two
	// answers, and until now both of them lived in another terminal.
	//
	// `a` and `d` are the verbs' own initials, and the asymmetry between them
	// is the point. Deny carries no second input, so it runs on the keypress
	// — the safe answer is one key, and a denial the operator did not mean
	// costs the agent a retry. Allow declares an optional --ttl, so runAction
	// opens the form for it: granting access stops for a confirmation, which
	// is the direction that cannot be taken back once a secret has been read.
	//
	// `d` also means "done" on the task lists, and net.hosts.list avoided
	// exactly that overlap. It is deliberate here: this screen is a security
	// prompt rather than another list, both keys spell their own verb, and
	// the mistake the overlap could produce — denying a call meant to be
	// allowed — is the recoverable one.
	"agent.pending": {
		// enter is "show" on every list in this table, and a parked call has
		// more to show than a row can hold — what it would actually do, most
		// of all. Reading before answering is the point, so the key that
		// opens the detail is the one already in everybody's fingers.
		{"enter", "show", "agent.show", srcRow},
		{"a", "allow", "agent.allow", srcRow},
		{"d", "deny", "agent.deny", srcRow},
	},
	// And the two answers again from the detail page, so reading it does not
	// mean going back to the list to act on what you read.
	"agent.show": {
		{"a", "allow", "agent.allow", srcSelf},
		{"d", "deny", "agent.deny", srcSelf},
	},
	// The tile says how many calls are waiting; these are the two places to
	// go from there. `g` because l is navigation and every other letter in
	// "log" is spoken for.
	"agent.overview": {
		{"w", "waiting", "agent.pending", srcNone},
		{"g", "log", "agent.log", srcNone},
	},
	// `v` reveals, and the argument for it is the argument that was originally
	// made against it, followed through.
	//
	// This table used to say: no reveal action, because "a secret shown
	// because a key was pressed on a list is a secret shown by accident" —
	// `kv get` asks for it by name, which is the point at which you meant to.
	// The reasoning is right and the conclusion did not follow, because it
	// measured the wrong thing. **The friction that makes a reveal deliberate
	// is not the typing; it is the unlock.** Every kv action opens the unlock
	// form on the way — the passphrase and identity are inputs like any other,
	// so `fieldsAfter` always has something left to ask — and an operator who
	// pressed `v` by accident is looking at a form naming the entry, not at
	// its value. The value then arrives on its own result page, titled with
	// the entry it belongs to, rather than in a cell of a list somebody was
	// scrolling.
	//
	// `c` was the tell. Copying is the same act with a smaller audience —
	// The catalogue classifies it identically for exactly that reason, "a value on
	// the clipboard has been revealed" — and it has been a row action here
	// since the beginning. The old comment argued the difference (no
	// scrollback, no screen share, undone by the next copy), and that
	// difference is real; what it does not support is making the *other* half
	// unreachable from the screen an operator is already on, which sent people
	// to a second terminal for a secret they had already unlocked the store
	// for.
	//
	// What stays refused is the thing actually worth refusing: nothing on this
	// screen puts a value in a row. `kv list` shows names, kinds and
	// descriptions, and the entry's page shows its metadata; a value appears
	// only where somebody asked for that one entry.
	//
	// kv.edit is still absent, for an unrelated reason: it hands the terminal
	// to $EDITOR, and the terminal is what this program is drawing on.
	"kv.list": {
		{"enter", "show", "kv.show", srcRow},
		{"v", "reveal", "kv.get", srcRow},
		{"c", "copy", "kv.copy", srcRow},
		{"a", "add", "kv.set", srcNone},
		{"s", "set", "kv.set", srcRow},
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
		{"v", "reveal", "kv.get", srcSelf},
		{"c", "copy", "kv.copy", srcSelf},
		{"s", "set", "kv.set", srcSelf},
		{"m", "rename", "kv.rename", srcSelf},
		{"x", "remove", "kv.rm", srcSelf},
		{"a", "add", "kv.set", srcNone},
	},
	// The detail pages act on the record they are already showing.
	"todo.show": {
		{"u", "update", "todo.edit", srcSelf},
		{"d", "done", "todo.done", srcSelf},
		{"o", "re-open", "todo.reopen", srcSelf},
		{"x", "remove", "todo.rm", srcSelf},
		{"a", "add", "todo.add", srcNone},
	},
	"note.show": {
		{"u", "update", "note.edit", srcSelf},
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
