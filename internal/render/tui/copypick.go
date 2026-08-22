package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The copy picker: when copySpecs names a value that exists in more than
// one row of a result — gen.password --count 5, or gen.overview's five
// recipes side by side — "c" opens this instead of doing nothing. There is
// no row-selection UI to point at one of the several another way: that
// needs a capability with row actions declared in capActionSpecs
// (dashboard.go), which a stateless generator has none of by construction —
// there is nothing to act on a generated value *with* besides copying it,
// which is exactly the gap this closes. One huh.Select naming every value
// verbatim; Enter copies whichever is highlighted, unprefixed and exact —
// the "N: " before it is a label for finding it on screen, never part of
// what lands on the clipboard.
//
// Reachable from two places: a result already open (modeResult) and a
// tile's own preview on the dashboard, without opening it first — the
// values are the same either way (m.result.view and a tile's own view are
// both just a view.View), so one picker serves both. What differs is where
// "done" goes back to, which is why the capability and the return mode
// travel with the form rather than being read off m.current/m.origin: a
// tile does not set m.current at all, and m.origin already means something
// else (where the *result itself* was opened from, not where this picker
// was opened from).

// copyPickForm holds the choice between the values copyChoices found.
type copyPickForm struct {
	form  *huh.Form
	value string
	// cap is whose result this is, for the panel head — not read off
	// m.current, which a tile-opened picker never touched.
	cap plugin.Capability
	// returnTo is where confirming or cancelling goes back to: modeResult
	// for a result on screen, modeDashboard for a tile.
	returnTo mode
}

func newCopyPickForm(values []string, cap plugin.Capability, returnTo mode) *copyPickForm {
	cp := &copyPickForm{cap: cap, returnTo: returnTo}
	opts := make([]huh.Option[string], len(values))
	for i, v := range values {
		opts[i] = huh.NewOption(fmt.Sprintf("%d: %s", i+1, v), v)
	}
	cp.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title("copy which value?").
			Options(opts...).
			Value(&cp.value),
	))
	return cp
}

// startCopyPick opens the picker for spec against v — m.result.view from
// modeResult, or a tile's own view from the dashboard. cap is whichever
// capability v belongs to, for the panel head.
func (m Model) startCopyPick(spec copySpec, cap plugin.Capability, v view.View, returnTo mode) (tea.Model, tea.Cmd) {
	values, ok := copyChoices(spec, v)
	if !ok {
		return m, nil
	}
	m.copyPick = newCopyPickForm(values, cap, returnTo)
	m.mode = modeCopyPick
	m.fitCopyPick()
	return m, m.copyPick.form.Init()
}

// fitCopyPick sizes the embedded huh form to the panel frame, mirroring
// fitForm/fitThemeForm.
func (m *Model) fitCopyPick() {
	if m.copyPick == nil {
		return
	}
	if m.width > 0 {
		m.copyPick.form = m.copyPick.form.WithWidth(min(m.width-6, 80))
	}
	if m.height > 0 {
		m.copyPick.form = m.copyPick.form.WithHeight(max(m.height-5, 6))
	}
}

// updateCopyPick drives the embedded huh form and hands off to
// afterCopyPickUpdate, mirroring updateForm/updateThemeForm's
// drive-then-dispatch shape for this one mode.
func (m Model) updateCopyPick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.copyPick == nil {
		m.mode = modeResult
		return m, nil
	}
	model, cmd := m.copyPick.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.copyPick.form = f
	}
	return m.afterCopyPickUpdate(cmd)
}

// afterCopyPickUpdate dispatches on the form's state after it was driven
// forward by one message — a real keypress from updateCopyPick, or several
// synthetic ones from fastSubmitCopyPick — the same either way. Split out
// for the same reason afterFormUpdate/afterThemeFormUpdate are: a fast
// path can drive the form ahead of the normal per-keypress loop and still
// land on the one place completion is handled.
func (m Model) afterCopyPickUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.copyPick.form.State {
	case huh.StateCompleted:
		return m.confirmCopyPick()
	case huh.StateAborted:
		return m.closeCopyPick()
	}
	return m, cmd
}

// fastSubmitCopyPick accepts the highlighted choice immediately — shift+enter's
// meaning here is identical to plain enter's, since the picker is always a
// single field, but it is wired in anyway so the shortcut means the same
// thing on every form-shaped screen rather than a set someone has to learn.
func (m Model) fastSubmitCopyPick() (tea.Model, tea.Cmd) {
	if m.copyPick == nil {
		return m, nil
	}
	m.copyPick.form = advanceFormBySyntheticEnter(m.copyPick.form)
	return m.afterCopyPickUpdate(nil)
}

// confirmCopyPick copies whichever value the form landed on and returns to
// wherever the picker was opened from.
func (m Model) confirmCopyPick() (tea.Model, tea.Cmd) {
	value := m.copyPick.value
	next, cmd := m.closeCopyPick()
	nm := next.(Model)
	if verr := copyValueToClipboard(value); verr != nil {
		nm.flash = "not copied: " + verr.Error()
	} else {
		nm.flash = "copied value"
	}
	return nm, cmd
}

// closeCopyPick dismisses the picker without copying anything, back to
// returnTo — restarting tile refresh when that is the dashboard, the same
// way closeToOrigin does for every other screen that can return there.
// Refresh is already paused while the picker is open: the tick handler
// only re-arms itself while m.mode == modeDashboard (tui.go), so a tile's
// values cannot change out from under a choice mid-pick.
func (m Model) closeCopyPick() (tea.Model, tea.Cmd) {
	returnTo := m.copyPick.returnTo
	m.copyPick = nil
	m.mode = returnTo
	if returnTo == modeDashboard {
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg)
	}
	return m, nil
}

// copyPickView frames the picker the same way formView frames a capForm —
// same panel, same capHead, so choosing which value to copy reads as part
// of the result it is choosing from rather than a screen of its own.
func (m Model) copyPickView() string {
	if m.copyPick == nil {
		return ""
	}
	footer := fitHintBar(m.width, footerMaxLines,
		labelled(bindOpen, "copy"), item(bindFastSubmit), labelled(bindBack, "cancel"))
	return panel(capHead(m.copyPick.cap), "\n"+m.copyPick.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}
