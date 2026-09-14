package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The destructive gate on this surface: what a run would do, shown before it
// is allowed to.
//
// It used to be a Confirm field at the end of the form — "kv.rm is
// destructive — run it? This cannot be undone." — and the second sentence was
// written once for every capability, so it was false for kv.rm, whose own
// summary says the key stays restorable until purged, and for anything else
// with an undo. A confirmation that is sometimes wrong trains people to press
// through it. What a person can actually consent to is what the call will do,
// and every capability that changes something already knows how to say that:
// --dry-run. The consent queue shows an operator the same preview before they
// approve an agent's call (internal/mcp's propose); this is the person at the
// keyboard getting the same courtesy, on every destructive run.
//
// The dry run is trusted exactly as far as propose trusts it. A built-in's is
// rta's own code, held to sdktest's dry-run honesty in CI. A plugin from
// outside this binary makes a claim rta cannot check, and a dry run that lies
// is the one thing that must not happen before anyone has said yes — so an
// external capability is confirmed on its inputs alone, and the screen says
// so rather than showing nothing.

// previewMsg carries a finished dry run back into the update loop.
type previewMsg struct {
	cap     plugin.Capability
	view    view.View
	err     *view.Error
	elapsed time.Duration
	// seq identifies the run this preview belongs to, exactly as
	// resultMsg.seq does: a preview from a run the user walked away from
	// must not become a confirmation screen over whatever they walked to.
	seq int
}

// startConfirm opens the confirmation for running c with values: a dry run
// where one can be trusted, the inputs alone where it cannot.
//
// The run itself waits for enter on the screen this produces; until then
// nothing has been done, whatever the preview shows.
func (m *Model) startConfirm(c plugin.Capability, values map[string]any) tea.Cmd {
	m.current = c
	m.lastValues, m.lastYes = values, false
	if c.Run == nil || m.external(c) {
		m.showConfirm(resultMsg{cap: c, view: inputsOnly(c, values, m.external(c))})
		return nil
	}
	if m.cancelRun != nil {
		m.cancelRun()
	}
	// The same resolution the run will get, so what is previewed is what
	// would run. A connection that cannot be resolved is the run's own
	// failure, shown as startRun would show it: there is nothing to confirm
	// about a call that cannot be made.
	name, filled, conn, verr := m.resolveProfile(c, values)
	if verr != nil {
		m.runSeq++
		m.mode = modeResult
		m.result = resultMsg{cap: c, err: verr, seq: m.runSeq}
		m.renderResult()
		m.viewport.GotoTop()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancelRun = cancel
	m.runSeq++
	m.mode = modeRunning
	m.previewing = true
	return tea.Batch(m.spinner.Tick,
		runCmd(ctx, m.runSeq, c, withoutPicker(c, values), true, m.configFor(c), name, filled, conn, true))
}

// showConfirm puts the confirmation on screen with r as its body — a dry
// run's view, its error, or the inputs-only page.
func (m *Model) showConfirm(r resultMsg) {
	// Cleaned on the way in, for resultMsg's reason: what the model stores
	// and what the screen shows must be one string.
	r.view = view.MapStrings(r.view, textclean.Terminal)
	r.err = view.MapErrorStrings(r.err, textclean.Terminal)
	m.previewing = false
	m.result = r
	m.mode = modeConfirm
	m.renderResult()
	m.viewport.GotoTop()
}

// external reports whether c came from a plugin outside this binary.
func (m Model) external(c plugin.Capability) bool {
	if m.reg == nil {
		return false
	}
	o, ok := m.reg.Origin(plugin.Namespace(c.ID))
	return ok && o.External()
}

// inputsOnly is the confirmation body when there is no dry run to show: the
// values the call will run with, credentials masked, and why that is all.
func inputsOnly(c plugin.Capability, values map[string]any, external bool) view.View {
	var kv view.KeyValue
	if p, ok := values[profileInput]; ok && p != nil && p != "" {
		kv.Pairs = append(kv.Pairs, view.Pair{Key: "profile", Value: fmt.Sprint(p)})
	}
	for _, f := range c.Inputs {
		v, ok := values[f.Name]
		if !ok || v == nil || v == "" {
			continue
		}
		kv.Pairs = append(kv.Pairs, view.Pair{Key: f.Name, Value: fmt.Sprint(v)})
		if f.Type.Sensitive() {
			kv.Redacted = append(kv.Redacted, f.Name)
		}
	}
	why := "No preview: this capability has no run that does nothing, so what it would do is not something rta can show first."
	if external {
		why = fmt.Sprintf("No preview: %s comes from a plugin outside this binary, and rta does not run a "+
			"plugin's dry run before you have confirmed — what it would do is the plugin's own claim, and a "+
			"claim is not something to act on before anyone has said yes. `rta %s --dry-run` asks it at the "+
			"command line, on your say-so.", c.ID, strings.Join(c.Words(), " "))
	}
	note := view.Text{Body: why}
	if len(kv.Pairs) == 0 {
		return note
	}
	return view.Sections{Items: []view.Section{
		{Title: "will run with", View: kv},
		{Title: "preview", View: note},
	}}
}

// confirmKeys is the confirmation screen's own vocabulary: enter runs, e
// reopens the inputs, esc is a cancel that nothing follows. Scrolling is the
// viewport's, as on a result.
func (m Model) confirmKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "enter":
		m.lastYes = true
		return m, m.startRun(m.current, m.lastValues, true), true
	case "e":
		if hasInputs(m.current) {
			nm, cmd := m.startForm(m.current, m.lastValues)
			return nm, cmd, true
		}
		return m, nil, true
	case "esc":
		nm, cmd := m.cancelConfirm()
		return nm, cmd, true
	case "q", "ctrl+c":
		return m, tea.Quit, true
	}
	return m, nil, false
}

// cancelConfirm leaves without running: back to the list the action came
// from, or to wherever the capability was opened.
func (m Model) cancelConfirm() (tea.Model, tea.Cmd) {
	m.refreshPending, m.subjectGone = false, false
	m.flash = "cancelled — nothing ran"
	if len(m.trail) > 0 {
		return m.reopenTop()
	}
	return m.closeToOrigin()
}

// confirmFooterItems is what the confirmation screen advertises.
func (m Model) confirmFooterItems() []hintItem {
	items := []hintItem{labelled(bindOpen, "run")}
	if hasInputs(m.current) {
		items = append(items, item(bindEdit))
	}
	return append(items, item(bindScroll), labelled(bindBack, "cancel"), item(bindQuit))
}

// confirmView frames the preview the way a result is framed, with the one
// difference stated in the border: nothing has happened yet.
func (m Model) confirmView() string {
	head := capHead(m.current)
	head.Note = "nothing has run yet"
	if m.result.elapsed > 0 {
		head.Right = m.result.elapsed.Round(time.Millisecond).String()
	}
	footer := m.footerFor(modeConfirm)
	return panel(head, m.viewport.View(), m.width, m.height-lipgloss.Height(footer), false) + "\n" + footer
}
