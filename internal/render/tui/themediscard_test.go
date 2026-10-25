package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// The theme editor threw its edits away on esc, and quit the whole screen on
// ctrl+c, without a word, where a capability form with something typed in it
// asks first. It asks in the same dialog, and an editor nobody touched closes
// at once as an untouched form does.

// themeEditor is the editor on the top-level block, holding stored.
func themeEditor(t *testing.T, stored map[string]string) Model {
	t.Helper()
	resetTheme(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 30
	m.themeForm, m.mode = newThemeForm(globalTarget(stored)), modeTheme
	m.themeForm.form.Init()
	m.fitThemeForm()
	return m
}

func TestEscOnAThemeEditorWithAColourTypedAsksFirst(t *testing.T) {
	m := press(t, strike(themeEditor(t, nil), "#123456"), "esc")
	if m.dialog == nil || m.dialog.id != dialogDiscardForm {
		t.Fatalf("esc with a colour typed asked %v, want the discard question", m.dialog)
	}
	if m.themeForm == nil || m.mode != modeTheme {
		t.Fatalf("the editor did not wait for the answer (form %v, mode %v)", m.themeForm != nil, m.mode)
	}
	kept := press(t, m, "n")
	if kept.dialog != nil || kept.themeForm == nil || kept.mode != modeTheme || !kept.themeForm.touched() {
		t.Errorf("keep editing did not return to the editor as it was (dialog %v, form %v, mode %v)",
			kept.dialog != nil, kept.themeForm != nil, kept.mode)
	}
	gone := press(t, m, "y")
	if gone.dialog != nil || gone.themeForm != nil || gone.mode != modeDashboard {
		t.Errorf("discard did not close the editor (dialog %v, form %v, mode %v)", gone.dialog != nil, gone.themeForm != nil, gone.mode)
	}
}

func TestCtrlCOnAThemeEditorWithAColourTypedAsksFirst(t *testing.T) {
	m := themeEditor(t, nil)
	m = strike(m, "#123456")
	out, cmd := m.Update(ctrlC)
	got := out.(Model)
	if got.dialog == nil || got.dialog.id != dialogDiscardForm || cmd != nil {
		t.Errorf("ctrl+c with a colour typed: dialog %v, a command %v; want the question and nothing run", got.dialog, cmd != nil)
	}
}

func TestEscOnAThemeEditorNobodyTouchedClosesAtOnce(t *testing.T) {
	m := press(t, themeEditor(t, map[string]string{"primary": "#123456"}), "esc")
	if m.dialog != nil || m.themeForm != nil || m.mode != modeDashboard {
		t.Errorf("esc on an untouched editor: dialog %v, form %v, mode %v, want it closed", m.dialog != nil, m.themeForm != nil, m.mode)
	}
}

// What the block holds on disk is the comparison, not an empty box: an editor
// opened on a stored colour is untouched until that colour changes.
func TestAThemeBoxBackToWhatItHeldIsNotAChange(t *testing.T) {
	m := themeEditor(t, nil)
	m = strike(m, "#1")
	if !m.themeForm.touched() {
		t.Fatal("a typed colour is not a change")
	}
	backspace := tea.KeyPressMsg{Code: tea.KeyBackspace}
	m = tap(tap(m, backspace), backspace)
	if m.themeForm.touched() {
		t.Errorf("a box typed in and emptied again reads as a change")
	}
}
