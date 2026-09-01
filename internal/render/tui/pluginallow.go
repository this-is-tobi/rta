package tui

import (
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/internal/plugintrust"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Allowing a plugin to read a credential location, from the pane that shows
// what it is asking for.
//
// The pane already named the gap — "needs kubeconfig and has not been allowed
// — `rta plugin allow weather`" — and then sent somebody to another window to
// act on it. That is the same shell-out the trust key removed, one permission
// further along, and the same argument applies: the digest, the artifact path
// and the list of locations are all on screen while the decision is made,
// where the command shows them only afterwards.
//
// # Why this is a form and `t` is a keypress
//
// **The two decisions are not the same size, so they do not get the same
// control.** Approving an artifact says "these bytes may run", which is one
// yes/no about one thing already named on the row. Allowing is plural and
// asymmetric: a plugin declares several locations, and `a` as a bare key would
// hand over *every* location it declared, from a cursor position, with the
// row's own text as the only description of what was granted. A keypress that
// grants a set nobody enumerated is exactly the shape this codebase refuses
// elsewhere — granting stops for a form.
//
// The form also makes withdrawal expressible, which a key cannot: plugintrust
// .Allow states the whole grant rather than adding to it, so a checkbox list
// submitted with one box cleared *is* the disallow, and there is no second
// control to learn.

// allowFields turns a plugin's declared needs into one checkbox each, seeded
// from what it is allowed right now — so the form opens showing the current
// grant rather than an empty one somebody would have to rebuild from memory.
func allowFields(row pluginRow, allowed []string) []plugin.Field {
	fields := make([]plugin.Field, 0, len(row.plugin.Needs))
	for _, n := range row.plugin.Needs {
		on := false
		for _, a := range allowed {
			if a == string(n) {
				on = true
				break
			}
		}
		fields = append(fields, plugin.Field{
			Name: string(n), Type: plugin.Bool, Default: on,
			Help: "let " + row.plugin.Name + " read " + string(n),
		})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
	return fields
}

// startAllowForm opens the checkbox list for row.
func (m Model) startAllowForm(row pluginRow) (tea.Model, tea.Cmd) {
	// Ordered from the most fundamental refusal outwards, so the message names
	// the first thing that is actually in the way rather than the last.
	switch {
	case !row.external() && !row.waiting:
		m.flash = row.plugin.Name + " is built into rta — it reads what rta reads, " +
			"and there is no separate artifact to allow"
		return m, nil
	case row.waiting:
		// plugintrust.Allow refuses this case too, but its error arrives after
		// the operator has filled in a form, and the answer is a different key
		// on the same row.
		m.flash = row.plugin.Name + " has not been approved to run yet — press t first, " +
			"because running at all is the decision that comes before reading anything"
		return m, nil
	case len(row.plugin.Needs) == 0:
		m.flash = row.plugin.Name + " does not ask to read any credential location"
		return m, nil
	}

	fields := allowFields(row, plugintrust.Load().Allowed(row.origin.Digest))
	synth := plugin.Capability{
		ID:      row.plugin.Name + ".allow",
		Summary: "credential locations " + row.plugin.Name + " may read",
		// Write rather than Read: this changes a standing permission on this
		// machine. Not Destructive — nothing is deleted, and clearing every box
		// is a withdrawal that the next form submission can undo.
		Safety: plugin.Write,
		Inputs: fields,
	}
	m.current = synth
	m.trail = nil
	m.form = newCapForm(synth, fields, nil, true, nil)
	m.form.allowTarget = row.plugin.Name
	m.form.allowDigest = row.origin.Digest
	m.origin = modePlugins
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// saveAllowForm writes the submitted set as the whole grant.
func (m Model) saveAllowForm() (tea.Model, tea.Cmd) {
	name, digest := m.form.allowTarget, m.form.allowDigest
	values := m.form.values()
	fields := m.form.fields
	m.form = nil
	m.mode = modePlugins

	var locations []string
	for _, f := range fields {
		if on, _ := values[f.Name].(bool); on {
			locations = append(locations, f.Name)
		}
	}
	if verr := plugintrust.Allow(digest, locations); verr != nil {
		m.flash = "not changed: " + verr.Message
		return m, nil
	}
	// Rebuilt rather than patched: the pane reads the trust file once per
	// build, so the row's granted/ungranted split is stale the moment this
	// writes, and a screen still showing the old answer after a permission
	// change is the worst possible time to be out of date.
	m.plugins = pluginRows(m.reg, m.dash, m.untrusted)
	m.flash = allowFlash(name, locations)
	return m, nil
}

// allowFlash says what the grant now is, in full.
//
// Stating the resulting set rather than the delta, because that is what was
// actually written: Allow replaces, so "added kubeconfig" would describe an
// operation that did not happen and hide the locations that were dropped by
// being left unchecked.
func allowFlash(name string, locations []string) string {
	if len(locations) == 0 {
		return name + " may no longer read any credential location — " +
			"it stays loaded until rta exits"
	}
	return name + " may read " + strings.Join(locations, ", ") + " — nothing else"
}
