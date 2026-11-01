package tui

import (
	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A picker: one choice between labelled values, asked over the screen it was
// opened from — which of several values to copy (copypick.go), which
// connection a tile being added is about (addpick.go), which order a result's
// table is read in (sort.go). What is asked and what
// the answer does belong to the kind. The rest is one screen: the form, its
// keys, its frame, its bar and where cancelling goes back to. The two kinds
// used to be two copies of it, alike line for line, and a third would have
// been a third copy.

// pickKind is what a picker asks, and so what its answer does.
type pickKind int

const (
	// pickCopy asks which of several values goes to the clipboard.
	pickCopy pickKind = iota
	// pickTile asks which connection a tile being added is about.
	pickTile
	// pickSort asks which order a result's table is read in (sort.go).
	pickSort
)

// verb is what enter does on the picker, as its bar says it.
func (k pickKind) verb() string {
	switch k {
	case pickTile:
		return "add"
	case pickSort:
		return "sort"
	}
	return "copy"
}

// noun is how the help overlay names the picker.
func (k pickKind) noun() string {
	switch k {
	case pickTile:
		return "add picker"
	case pickSort:
		return "sort picker"
	}
	return "copy picker"
}

// pickOption is one answer a picker offers: what its row reads, and the value
// choosing it gives.
type pickOption struct{ label, value string }

// pickForm is a picker open on screen.
type pickForm struct {
	kind  pickKind
	form  *huh.Form
	value string
	// cap is whose result or tile the choice is about, for the panel head —
	// not read off m.current, which a picker opened over a tile never touched.
	cap plugin.Capability
	// returnTo is where cancelling goes back to: the screen the picker was
	// opened over. Where an answer goes is the kind's — a tile lands on the
	// dashboard, with the new tile selected.
	returnTo mode
}

// newPickForm is a picker asking title of options, highlighted on value.
func newPickForm(kind pickKind, title, value string, options []pickOption, cap plugin.Capability, returnTo mode) *pickForm {
	p := &pickForm{kind: kind, value: value, cap: cap, returnTo: returnTo}
	opts := make([]huh.Option[string], len(options))
	for i, o := range options {
		opts[i] = huh.NewOption(o.label, o.value)
	}
	p.form = huh.NewForm(huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Options(opts...).
			Value(&p.value),
	)).WithTheme(formTheme()).WithShowHelp(false).WithShowErrors(false)
	return p
}

// openPick puts p on screen.
func (m Model) openPick(p *pickForm) (tea.Model, tea.Cmd) {
	m.pick = p
	m.mode = modePick
	m.fitPick()
	return m, p.form.Init()
}

// fitPick sizes the embedded huh form to the panel frame, mirroring
// fitForm/fitThemeForm.
func (m *Model) fitPick() {
	if m.pick == nil {
		return
	}
	if m.width > 0 {
		m.pick.form = m.pick.form.WithWidth(formWidth(m.width))
	}
	if m.height > 0 {
		m.pick.form = m.pick.form.WithHeight(max(m.height-5, 6))
	}
}

// updatePick drives the embedded huh form and hands off to afterPickUpdate,
// the drive-then-dispatch shape every form-driven mode shares.
func (m Model) updatePick(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.pick == nil {
		m.mode = modeDashboard
		return m, nil
	}
	model, cmd := m.pick.form.Update(msg)
	if f, ok := model.(*huh.Form); ok {
		m.pick.form = f
	}
	return m.afterPickUpdate(cmd)
}

// afterPickUpdate dispatches on the form's state after it was driven forward
// by one message — a real keypress from updatePick, or several synthetic ones
// from fastSubmitPick — the same either way. Split out for the same reason
// afterFormUpdate/afterThemeFormUpdate are: a fast path can drive the form
// ahead of the normal per-keypress loop and still land on the one place
// completion is handled.
func (m Model) afterPickUpdate(cmd tea.Cmd) (tea.Model, tea.Cmd) {
	switch m.pick.form.State {
	case huh.StateCompleted:
		return m.confirmPick()
	case huh.StateAborted:
		return m.closePick()
	}
	return m, cmd
}

// fastSubmitPick accepts the highlighted answer: shift+enter means the same as
// enter on a single-field form, wired so the shortcut means one thing on every
// form-shaped screen rather than a set someone has to learn.
func (m Model) fastSubmitPick() (tea.Model, tea.Cmd) {
	if m.pick == nil {
		return m, nil
	}
	m.pick.form = advanceFormBySyntheticEnter(m.pick.form)
	return m.afterPickUpdate(nil)
}

// confirmPick does what the answer is for.
func (m Model) confirmPick() (tea.Model, tea.Cmd) {
	p := *m.pick
	switch p.kind {
	case pickTile:
		m.pick = nil
		return m.addTile(p.cap, p.value)
	case pickSort:
		next, cmd := m.closePick()
		return next.(Model).sortResult(orderOfPick(p.value)), cmd
	}
	next, cmd := m.closePick()
	return next, tea.Batch(cmd, copyInBackground(p.value, copiedValue))
}

// closePick dismisses the picker without doing anything, back to the screen it
// was opened over — restarting tile refresh when that is the dashboard, the
// same way closeToOrigin does for every other screen that can return there.
// Refresh is already paused while a picker is open: the tick handler only
// re-arms itself while m.mode == modeDashboard (tui.go), so a tile's values
// cannot change out from under a choice mid-pick.
func (m Model) closePick() (tea.Model, tea.Cmd) {
	returnTo := m.pick.returnTo
	m.pick = nil
	m.mode = returnTo
	if returnTo == modeDashboard {
		m.tickGen++
		return m, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor)
	}
	return m, nil
}

// pickKeys are the picker's own keys, ahead of the form: esc clears a filter
// before it cancels, the way a combobox closes its popup before the dialog
// around it.
func (m Model) pickKeys(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch msg.String() {
	case "esc":
		if m.pick != nil && filterHoldsEsc(m.pick.form) {
			m.pick.form = clearFilter(m.pick.form)
			return m, nil, true
		}
		nm, cmd := m.closePick()
		return nm, cmd, true
	case "ctrl+c":
		return m.quit()
	case "shift+enter", "alt+enter", "ctrl+s":
		nm, cmd := m.fastSubmitPick()
		return nm, cmd, true
	}
	return m, nil, false
}

// pickView frames the picker under the capability's own head, the way formView
// frames a capForm, so the choice reads as part of the result or the
// capability it is about rather than a screen of its own.
func (m Model) pickView() string {
	if m.pick == nil {
		return ""
	}
	footer := m.footerFor(modePick)
	return panel(capHead(m.pick.cap), "\n"+m.pick.form.View(), m.width, m.height-lipgloss.Height(footer), true) + "\n" + footer
}
