package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Driving a capability's form from the model's side: which fields a form
// opens with and what seeds them, the prefill two-stage split, and what
// happens to a form on every message until it completes. The widget itself
// is capForm (form.go); this is the shell around it.

// startForm opens the input form. Prefill-capable capabilities stage it:
// identity fields first, then the remaining fields seeded with the record's
// current values — editing in place, not typing blind.
//
// prev is the last run's values, split here into the two halves that get
// treated differently (see asked/unasked). The carry half holds what no field
// can ask about and must not be dropped: a form rebuilds the value map from
// its own bindings, so `e` used to lose it — pressing `D` to turn detail off
// and then `e` to change a filter came back detailed. The shown half is what
// the boxes open on, which is what makes `e` mean "edit" rather than "type it
// all again". Opening a fresh capability passes nil and starts from its own
// defaults rather than inheriting anything from whatever ran last.
func (m Model) startForm(c plugin.Capability, prev map[string]any) (tea.Model, tea.Cmd) {
	// A Secret field's plaintext must never carry forward into a new form
	// the way an ordinary value does. withoutSecrets already exists for
	// exactly this reason on the profile-resolved seeding path (active.go);
	// this is the other path that feeds newCapForm — the one "edit inputs"
	// (`e`, dispatch.go) actually uses, with prev being m.lastValues, which
	// includes whatever a Secret field was last submitted as. Stripped
	// before carry and shown are derived, so neither this form nor a
	// Prefill-staged second one downstream can reseed it: without this, a
	// credential typed for one target reopened pre-filled — masked as dots,
	// indistinguishable from any other default — and could be silently
	// resubmitted to whatever the operator had just edited the rest of the
	// form to point at instead.
	prev = withoutSecrets(c, prev)
	carry, shown := unasked(c, prev), asked(c, prev)
	keys, rest := keyFields(c)
	switch {
	case c.Prefill != nil && len(keys) > 0 && len(rest) > 0:
		// Stage one's values become stage two's base, so carrying here is
		// enough for both. No picker: these are the identity fields, and the
		// environment is asked for on the stage that has something to aim.
		// No derived marks either — this form is not final, so a value
		// dropped here would be missing from stage two's base rather than
		// re-derived (displayed's own final gate backstops the same rule).
		seed, _, _ := m.formSeed(c, shown, m.pickedProfile(c, carry))
		m.form = newCapForm(c, keys, seed, false, carry)
	case c.Prefill != nil && len(keys) == 0:
		defaults, err := prefill(c, nil)
		if err != nil {
			return m.formError(c, err)
		}
		m.form = m.runForm(c, c.Inputs, over(shown, defaults), carry)
	default:
		m.form = m.runForm(c, c.Inputs, shown, carry)
	}
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// startFormWith opens a form for the fields base does not cover, seeded by
// Prefill when the capability offers it — the row-action edit experience.
//
// prev is what the view this action was pressed in ran with. Prefill goes on
// top of it: both describe the same form, but Prefill is about the record the
// cursor is on and prev is about the listing it was found in, so where they
// name the same input the record's own value is the more specific answer.
func (m Model) startFormWith(c plugin.Capability, base, prev map[string]any) (tea.Model, tea.Cmd) {
	// Same reason as startForm: prev is a prior run's values verbatim, which
	// includes whatever a Secret field was last submitted as, and it feeds
	// the same newCapForm that would bind it into a reopened, pre-filled box.
	prev = withoutSecrets(c, prev)
	defaults := prev
	if c.Prefill != nil && len(base) > 0 {
		d, err := prefill(c, base)
		if err != nil {
			m.refreshPending = false
			return m.formError(c, err)
		}
		defaults = over(prev, d)
	}
	m.current = c
	m.form = m.runForm(c, fieldsAfter(c, base), defaults, base)
	m.fitForm()
	m.mode = modeForm
	return m, m.form.form.Init()
}

// firstPositional returns the leading positional input, if there is one.
func firstPositional(c plugin.Capability) []plugin.Field {
	for _, f := range c.Inputs {
		if f.Positional {
			return []plugin.Field{f}
		}
	}
	return nil
}

// keyFields returns the required positional inputs — the identity of the
// record a Prefill-capable capability works on.
func keyFields(c plugin.Capability) (keys, rest []plugin.Field) {
	for _, f := range c.Inputs {
		if f.Positional && f.Required {
			keys = append(keys, f)
		} else {
			rest = append(rest, f)
		}
	}
	return keys, rest
}

// prefill fetches current values for a capability's editable fields.
func prefill(c plugin.Capability, base map[string]any) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Prefill(ctx, plugin.NewRequest(base, false, false).WithSurface(plugin.SurfaceTUI))
}

// unasked returns the request values no field of c can carry back — the
// reserved inputs, which every surface sets by some means other than asking.
// "detail" is the only one today; the rule is written against the property
// rather than the name, because a second reserved input would otherwise
// reintroduce the same bug silently. The environment picker rides along too:
// it is not one of c's inputs, and startRun needs it to aim the run.
func unasked(c plugin.Capability, values map[string]any) map[string]any {
	return partition(c, values, false)
}

// asked is the other half: the values c does declare fields for, which belong
// in the boxes rather than in the carry.
//
// The two halves are treated differently because they are different things. A
// value no field can carry has to travel in `base` or it is lost outright; a
// value the form *will* ask about belongs where it can be seen and changed.
// Seeding the boxes was the half that was missing, and it cost real work:
// "edit inputs" on `s3 object list --bucket mine` came back with an empty
// bucket, and — worse, because it is silent and destructive — a row action on
// `net hosts list --file ./container/hosts` opened a removal form pointed at
// the *system* hosts file, because the box for `file` was empty and empty
// means the default.
func asked(c plugin.Capability, values map[string]any) map[string]any {
	return partition(c, values, true)
}

func partition(c plugin.Capability, values map[string]any, declared bool) map[string]any {
	if len(values) == 0 {
		return nil
	}
	isInput := make(map[string]bool, len(c.Inputs))
	for _, f := range c.Inputs {
		isInput[f.Name] = true
	}
	var out map[string]any
	for k, v := range values {
		if isInput[k] != declared {
			continue
		}
		if out == nil {
			out = map[string]any{}
		}
		out[k] = v
	}
	return out
}

// continuing is what a form should open showing when it carries on from the
// result already on screen: what that result was produced with, for the inputs
// the capability it is about to run actually declares.
//
// Restricted to one namespace, which is a rule rather than an observation
// about today's action table: a row action reaches another capability of the
// same plugin (`net hosts list` → `net hosts rm`), where an input of the same
// name is the same thing by the convention config resolution already relies
// on. Across plugins it would be a guess, and a guess that pre-fills a
// destructive form is not a guess to make.
//
// Read before m.current moves, so "where this came from" is still the view on
// screen rather than the capability about to replace it.
func (m Model) continuing(c plugin.Capability) map[string]any {
	if m.current.ID == "" || plugin.Namespace(c.ID) != plugin.Namespace(m.current.ID) {
		return nil
	}
	return asked(c, m.lastValues)
}

// over returns base with top's entries laid on it, without touching either.
func over(base, top map[string]any) map[string]any {
	if len(top) == 0 {
		return base
	}
	out := make(map[string]any, len(base)+len(top))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range top {
		out[k] = v
	}
	return out
}

// fieldsAfter returns the capability inputs not already covered by base.
func fieldsAfter(c plugin.Capability, base map[string]any) []plugin.Field {
	var out []plugin.Field
	for _, f := range c.Inputs {
		if _, ok := base[f.Name]; !ok {
			out = append(out, f)
		}
	}
	return out
}

// fitForm sizes the embedded huh form to the panel frame.
//
// The height is the important half. A form is as tall as the capability has
// inputs — `kv set` asks eight questions — and a terminal is as tall as it
// is; without a bound the last fields render off-screen, including the
// destructive confirmation, which is the one nobody may miss. Given a height,
// huh scrolls its group instead, so every field stays reachable on a laptop
// with a split terminal.
// formWidth is how wide a form may be in a terminal of the given width.
//
// It used to be `min(width-6, 80)`, with the comment "80 is plenty on wide
// screens". It is not: on a 190-column terminal that left the form using
// under half the pane, and a one-sentence field description — "which Secret
// and key, as <secret>/<key> — tab completes; listing keys reads the Secret
// from the coordinate's namespace" — broke across two lines with the rest of
// the window empty beside it. The wrap read as a layout bug because it was
// one: the text was fitted to a bound that had nothing to do with the space
// available.
//
// **Bounded rather than unbounded, though**, because "use the whole terminal"
// is the other way to get this wrong. Measure is a readability property, not
// a space-filling one: past roughly 120 characters the eye loses its place
// returning to the start of the next line, and a form stretched across a
// 240-column terminal puts a field's label and its input at opposite ends of
// the desk. So this takes the room it is given, up to the point where more
// room stops being an improvement.
//
// The 6 is the panel's border and padding, which the form does not own.
func formWidth(width int) int {
	// The upper end of comfortable measure for technical prose. Wider is
	// harder to read, not easier.
	const maxMeasure = 120
	return min(width-6, maxMeasure)
}

func (m *Model) fitForm() {
	if m.form == nil {
		return
	}
	if m.width > 0 {
		m.form.form = m.form.form.WithWidth(formWidth(m.width))
	}
	if m.height > 0 {
		// Panel border (2) + the blank lead-in line (1) + footer (1) + a row
		// of slack, since huh sizes its own help line. The floor keeps a very
		// short terminal usable rather than empty.
		m.form.form = m.form.form.WithHeight(max(m.height-5, 6))
	}
}

// formError surfaces a staging failure (e.g. unknown id) as a result pane.
func (m Model) formError(c plugin.Capability, err error) (tea.Model, tea.Cmd) {
	m.form = nil
	m.current = c
	m.result = resultMsg{cap: c, err: view.AsError(err, c.ID+".failed")}
	m.mode = modeResult
	m.renderResult()
	m.viewport.GotoTop()
	return m, nil
}

// updateForm drives the embedded huh form and dispatches on completion.
func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.form == nil {
		m.mode = modeBrowse
		return m, nil
	}
	model, cmd := m.form.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.form.form = f
	}
	return m.afterFormUpdate(cmd)
}

// afterFormUpdate dispatches on the form's state after it was driven
// forward by one message — a real keypress from updateForm, or several
// synthetic ones from fastSubmitForm. Split out so both land on the same
// completion logic rather than the fast path needing its own copy of it.
func (m Model) afterFormUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	// Before the completion dispatch: a picker that moved re-seeds the form
	// on the environment it now names (reseedOnPickerMove), and the old
	// form's pending commands die with it.
	if nm, c, rebuilt := m.reseedOnPickerMove(); rebuilt {
		return nm, c
	}
	// And the connection editor on the plugin it is about, which decides the
	// `set:` boxes below it exactly as the picker decides the run form's.
	if nm, c, rebuilt := m.reseedOnConnPluginChange(); rebuilt {
		return nm, c
	}
	switch m.form.form.State {
	case huh.StateCompleted:
		if m.form.configTarget != "" {
			return m.saveConfigForm()
		}
		if m.form.allowTarget != "" {
			return m.saveAllowForm()
		}
		if m.form.credentialEditing {
			return m.saveCredentialForm()
		}
		if m.form.connEditing {
			return m.saveConnForm()
		}
		if m.form.profileEditing {
			return m.saveProfileForm()
		}
		if !m.form.final {
			// Identity collected: fetch current values and open the edit
			// stage seeded with them. A fast-submitted first stage stops
			// here rather than driving the second stage too — its fields
			// are not on screen yet to have current values worth accepting.
			base := m.form.values()
			defaults, err := prefill(m.current, base)
			if err != nil {
				return m.formError(m.current, err)
			}
			_, rest := keyFields(m.current)
			m.form = m.runForm(m.current, rest, defaults, base)
			m.fitForm()
			return m, m.form.form.Init()
		}
		if !m.form.confirmed() {
			// Destructive run declined: back to where we came from, nothing
			// happened. Fast-submitting a destructive form lands here too,
			// not on the run below — a Confirm field's own default is the
			// negative, so racing through it without touching it declines,
			// the same as leaving it alone and pressing enter would.
			m.flash = ""
			return m.closeForm()
		}
		m.lastValues = m.form.values()
		m.lastYes = m.form.confirmed() && m.current.Safety == plugin.Destructive
		m.form = nil
		return m, m.startRun(m.current, m.lastValues, m.lastYes)
	case huh.StateAborted:
		return m.closeForm()
	}
	return m, cmd
}

// fastSubmitForm accepts whatever is currently bound across every
// remaining field, exactly as pressing enter on each in turn would.
func (m Model) fastSubmitForm() (tea.Model, tea.Cmd) {
	if m.form == nil {
		return m, nil
	}
	m.form.form = advanceFormBySyntheticEnter(m.form.form)
	return m.afterFormUpdate(nil)
}

// formView frames the input form in an accent panel.
func (m Model) formView() string {
	if m.form == nil {
		return ""
	}
	footer := m.footerFor(modeForm)
	// The panel takes the whole screen minus the footer, so the frame is a
	// guarantee and not an estimate: huh scrolls its fields inside it, and
	// nothing can render past the last row whatever the field mix turns out
	// to cost.
	return panel(capHead(m.current), "\n"+m.form.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}

// closeForm dismisses the current form: back to the actionable view it was
// opened from, or to the origin.
func (m Model) closeForm() (tea.Model, tea.Cmd) {
	m.form = nil
	m.refreshPending, m.subjectGone = false, false
	if len(m.trail) > 0 {
		return m.reopenTop()
	}
	return m.closeToOrigin()
}
