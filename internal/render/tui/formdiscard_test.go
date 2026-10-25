package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// esc and ctrl+c threw away a half-typed form without a word, where `U` on an
// unlocked store asks first for an action that costs one passphrase to undo. A
// form where something differs from what it opened with asks, in the dialog
// every other question is asked in; one nobody touched closes at once.

var ctrlC = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}

// quitFromADirtyForm is ctrl+c as a form with something typed in it meets it: the
// first asks, and the second quits from the question.
func quitFromADirtyForm(t *testing.T, tm *teatest.TestModel) {
	t.Helper()
	tm.Send(ctrlC)
	waitFor(t, tm, "Discard what you typed?")
	quit(t, tm)
}

// discardForm is the form of retypeCap on a dashboard, the cursor on its first box.
func discardForm(t *testing.T) Model {
	t.Helper()
	m := retypeForm(t, nil)
	m.origin = modeDashboard
	m.width, m.height = 100, 30
	m.fitForm()
	return m
}

func TestEscOnAFormWithSomethingTypedAsksBeforeItDiscardsIt(t *testing.T) {
	m := press(t, strike(discardForm(t), "9"), "esc")

	if m.dialog == nil || m.dialog.id != dialogDiscardForm {
		t.Fatalf("esc on a form with a 9 typed in it asked %v, want the question", m.dialog)
	}
	if m.form == nil || m.mode != modeForm {
		t.Fatalf("the form did not wait for the answer (form %v, mode %v)", m.form != nil, m.mode)
	}
	if got := boxText(m, "limit"); got != "9" {
		t.Errorf("the question cost what was typed: the box reads %q", got)
	}
	if m.dialog.title != "Discard what you typed?" {
		t.Errorf("title = %q", m.dialog.title)
	}
	if m.dialog.focus != focusCancel {
		t.Errorf("the question opens on %d, want Keep editing: discarding is not the direction that loses nothing", m.dialog.focus)
	}
	shown := plain(m.View().Content)
	for _, want := range []string{"Discard what you typed?", "Discard", "Keep editing"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the screen does not show %q:\n%s", want, shown)
		}
	}
}

func TestEscOnAFormNobodyTouchedClosesAtOnce(t *testing.T) {
	m := press(t, discardForm(t), "esc")
	if m.dialog != nil || m.form != nil || m.mode != modeDashboard {
		t.Errorf("esc on an untouched form: dialog %v, form %v, mode %v, want it closed", m.dialog != nil, m.form != nil, m.mode)
	}
}

// The form is compared with what it opened with and not asked about every key:
// a box typed in and emptied again is the box it was.
func TestABoxBackToWhatItOpenedWithIsNotAChange(t *testing.T) {
	m := discardForm(t)
	m = pressEnter(t, pressEnter(t, m)) // limit, when: the box with nothing in it is note
	m = strike(m, "x")
	if !m.form.touched() {
		t.Fatal("a letter typed in a box is not a change")
	}
	m = tap(m, keyed(tea.KeyBackspace))
	if m.form.touched() {
		t.Error("a box emptied back to what it opened with still counts as a change")
	}
	if m = press(t, m, "esc"); m.dialog != nil || m.form != nil {
		t.Errorf("esc after undoing every change asked (dialog %v, form %v)", m.dialog != nil, m.form != nil)
	}
}

func TestTheAnswersToTheDiscardQuestion(t *testing.T) {
	ask := func() Model { return press(t, strike(discardForm(t), "9"), "esc") }
	kept := func(m Model, how string) {
		t.Helper()
		if m.dialog != nil || m.form == nil || m.mode != modeForm || boxText(m, "limit") != "9" {
			t.Errorf("%s: dialog %v, form %v, mode %v, want the form as it was left", how, m.dialog != nil, m.form != nil, m.mode)
		}
	}
	discarded := func(m Model, how string) {
		t.Helper()
		if m.dialog != nil || m.form != nil || m.mode != modeDashboard {
			t.Errorf("%s: dialog %v, form %v, mode %v, want the form closed to the dashboard", how, m.dialog != nil, m.form != nil, m.mode)
		}
	}

	kept(press(t, ask(), "enter"), "enter on Keep editing")
	kept(press(t, ask(), "esc"), "esc")
	kept(press(t, ask(), "n"), "n")
	kept(sent(t, ask(), keyMsg("N")), "N")
	discarded(press(t, ask(), "y"), "y")
	discarded(sent(t, ask(), keyMsg("Y")), "Y")
	discarded(press(t, press(t, ask(), "tab"), "enter"), "tab then enter on Discard")

	// A stray key does not answer: the form's own keys are not the question's.
	still := press(t, ask(), "x")
	if still.dialog == nil || boxText(still, "limit") != "9" {
		t.Errorf("a stray key answered the question or reached the box (dialog %v, box %q)", still.dialog != nil, boxText(still, "limit"))
	}
}

// ctrl+c quits from a form nobody touched, as it always did. Where something is
// typed it asks first, and from the question it quits, as it does from every one.
func TestCtrlCOnATouchedFormAsksAndOnAFreshOneQuits(t *testing.T) {
	fresh, cmd := discardForm(t).Update(ctrlC)
	if cmd == nil {
		t.Fatalf("ctrl+c on a fresh form did nothing (model %T)", fresh)
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("ctrl+c on a fresh form did not quit")
	}

	asked, cmd := strike(discardForm(t), "9").Update(ctrlC)
	if cmd != nil || asked.(Model).dialog == nil || asked.(Model).dialog.id != dialogDiscardForm {
		t.Fatalf("ctrl+c on a touched form: cmd %v, dialog %v, want the question", cmd != nil, asked.(Model).dialog)
	}
	_, cmd = asked.Update(ctrlC)
	if cmd == nil {
		t.Fatal("ctrl+c from the question did nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("ctrl+c from the question did not quit")
	}
}

// A picker's filter keeps esc for itself, as the previous key did: clearing it
// is not leaving the form, with something typed in the boxes above or not.
func TestEscStillClearsAPickersFilterBeforeAskingAboutTheForm(t *testing.T) {
	m := pickerForm(t)
	for _, k := range []string{"/", "2", "esc"} {
		m = press(t, m, k)
	}
	if m.dialog != nil || m.form == nil {
		t.Fatalf("esc in a filter asked about the form (dialog %v, form %v)", m.dialog != nil, m.form != nil)
	}
	if m = press(t, m, "esc"); m.dialog == nil || m.dialog.id != dialogDiscardForm {
		t.Errorf("esc with no filter held did not ask about a form with a name typed in it")
	}
}

// The form `e` opened is a detour from a result, and discarding it goes back
// to the result, as cancelling an untouched one does.
func TestDiscardingTheFormEOpenedGoesBackToTheResult(t *testing.T) {
	c := editableCap()
	rm := withResultHeld(t, demoModel(t, c), c, view.Text{Body: heldBody}, nil)
	form := strike(press(t, rm, "e"), "5")
	if !form.form.touched() {
		t.Fatal("a 5 typed over the 20 is not a change")
	}

	asked := press(t, form, "esc")
	if asked.dialog == nil || asked.mode != modeForm {
		t.Fatalf("esc did not ask (dialog %v, mode %v)", asked.dialog != nil, asked.mode)
	}
	back := press(t, asked, "y")
	if back.mode != modeResult || back.form != nil || !strings.Contains(plain(back.resultView()), heldBody) {
		t.Errorf("discarding left mode %v (form %v), want the result it came from", back.mode, back.form != nil)
	}
}

// What was typed is still typed when the form rebuilds itself under the
// person's hands: the second page of a form that asks in two, and the same boxes
// on another environment.
func TestAFormThatRebuildsItselfStillRemembersWhatWasTyped(t *testing.T) {
	staged := editableCap()
	staged.Inputs = []plugin.Field{
		{Name: "key", Type: plugin.String, Positional: true, Required: true},
		{Name: "value", Type: plugin.String},
	}
	staged.Prefill = func(context.Context, plugin.Request) (map[string]any, error) {
		return map[string]any{"value": "old"}, nil
	}
	m := demoModel(t, staged)
	m.origin = modeDashboard
	model, _ := m.startForm(staged, nil)
	first := model.(Model)
	first.form.form = startedForm(first.form)
	first.current = staged

	second := pressEnter(t, strike(first, "k"))
	if second.form == nil || !second.form.final {
		t.Fatalf("enter did not open the second page (form %v)", second.form != nil)
	}
	// esc on the second page goes back to the first (backToStageOne), and
	// leaving the form from there still knows the key was typed.
	back := press(t, second, "esc")
	if back.form == nil || back.form.final {
		t.Fatalf("esc on the second page did not go back to the first (form %v)", back.form != nil)
	}
	if asked := press(t, back, "esc"); asked.dialog == nil {
		t.Error("esc on the first page, come back to, forgot that a key was typed on it")
	}
}

// Opening a form is not changing it. Every capability's form, as it opens and the
// cursor is put where the form puts it, is the form it was built as: no widget
// writes its own answer back and has the shell take it for the person's.
func TestNoFormOpensAlreadyChanged(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	reg := realRegistry(t)
	m := New(reg, config.Dashboard{}, nil)
	opened := 0
	for _, c := range reg.Capabilities() {
		if !hasInputs(c) || c.Prefill != nil {
			continue
		}
		model, cmd := m.startForm(c, nil)
		nm, ok := model.(Model)
		if !ok || nm.form == nil {
			continue
		}
		nm.form.form = startedForm(nm.form)
		nm = feedBack(t, nm, cmd)
		opened++
		if nm.form.touched() {
			t.Errorf("%s opens already changed", c.ID)
		}
	}
	if opened < 50 {
		t.Errorf("only %d forms were opened, the check is not looking at the catalogue", opened)
	}
}

// The editors are forms too, and they open on what the file holds: an
// environment being edited is not changed by being opened, and the connection
// editor rebuilt on the plugin just named still remembers that a name was typed.
func TestTheEditorsOpenAsTheyWereAndARebuildRemembersWhatWasTyped(t *testing.T) {
	noHistory(t)
	m := twoPluginModel(t)
	m.pluginSel = pluginIndex(t, m, "plain")

	for name, open := range map[string]func() (tea.Model, tea.Cmd){
		"a new environment":  func() (tea.Model, tea.Cmd) { return m.startProfileForm("") },
		"an environment":     func() (tea.Model, tea.Cmd) { return m.startProfileForm("staging") },
		"a new entry in one": func() (tea.Model, tea.Cmd) { return m.startConnForm("") },
	} {
		next, _ := open()
		nm := next.(Model)
		if nm.form == nil {
			t.Fatalf("%s opened no form", name)
		}
		nm.form.form = startedForm(nm.form)
		if nm.form.touched() {
			t.Errorf("%s opens already changed", name)
		}
	}

	next, _ := m.startConnForm("")
	nm := next.(Model)
	nm.form.form = startedForm(nm.form)
	*nm.form.bindings[profilePluginField] = "db"
	after, _, rebuilt := nm.reseedOnConnPluginChange()
	if !rebuilt {
		t.Fatal("naming an installed plugin did not rebuild the form")
	}
	if got := after.(Model).form; !got.touched() {
		t.Error("the rebuilt editor forgot that a plugin was named in it")
	}
}
