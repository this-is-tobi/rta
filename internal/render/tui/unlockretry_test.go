package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/view"
)

// unlockSent is the unlock form opened with U, a passphrase typed into it and,
// when forText is not "", the length typed over its default, then sent: the
// model with the unlock in flight.
func unlockSent(t *testing.T, passphrase, forText string) Model {
	t.Helper()
	noHistory(t)
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 30
	nm, _ := m.open(unlockCapability("the store's passphrase"))
	m = nm.(Model)
	m.form.form = startedForm(m.form)
	m = pressEnter(t, typed(t, m, passphrase))
	if forText != "" {
		m = typed(t, m, forText)
	}
	m = pressEnter(t, m)
	if m.mode != modeRunning || m.submitted == nil {
		t.Fatalf("the unlock form was not sent: mode %v", m.mode)
	}
	return m
}

// A wrong passphrase opened an error page whose first key, r, ran the same wrong
// passphrase again, and the way to type another was e. It is answered on the form
// instead, as a login answers one: the passphrase box empty and the cursor in it,
// the length kept, and the reason on the bar — because the store's refusal names
// the passphrase as the input it is about.
func TestAWrongPassphraseIsAnsweredOnTheUnlockForm(t *testing.T) {
	m := unlockSent(t, "wrong-one", "2h")
	wrong := view.Errorf("kv.wrongpass", "could not decrypt the store").
		WithHint("wrong passphrase, or the store file is corrupt").WithInput("passphrase")

	out, cmd := m.Update(resultMsg{cap: m.current, err: wrong, seq: m.runSeq})
	got := out.(Model)
	if got.mode != modeForm || got.form == nil || got.form.cap.ID != unlockID {
		t.Fatalf("a wrong passphrase left mode %v (form %v), want the unlock form again", got.mode, got.form != nil)
	}
	if box := boxText(got, "passphrase"); box != "" {
		t.Errorf("the passphrase box holds %q, want it empty for the next try", box)
	}
	if box := boxText(got, "for"); box != "2h" {
		t.Errorf("the length box holds %q, want the 2h it was given", box)
	}
	if at := focusedName(t, feedBack(t, got, cmd)); at != "passphrase" {
		t.Errorf("the cursor is on %s, want the passphrase box", at)
	}
	if !strings.Contains(got.flashBad, "could not decrypt the store") {
		t.Errorf("the bar says %q, want the reason", got.flashBad)
	}
	if bar := plain(got.footerFor(modeForm)); !strings.Contains(bar, "could not decrypt the store") {
		t.Errorf("the form's bar does not show the reason:\n%s", bar)
	}
	// The wrong passphrase is not kept for r: the run it was sent with showed
	// nothing, and what r repeats is what was there before it.
	if _, kept := got.lastValues["passphrase"]; kept {
		t.Error("the wrong passphrase is still held for r")
	}

	// Anything else that goes wrong unlocking is still a page to read.
	m = unlockSent(t, "wrong-one", "")
	other, _ := m.Update(resultMsg{cap: m.current, err: view.Errorf("kv.read", "the store file cannot be read"), seq: m.runSeq})
	if other.(Model).mode != modeResult {
		t.Error("a store that cannot be read was not answered with a page")
	}
}

// esc on the form opened again closes it at once when what was typed was the
// passphrase alone: the box is empty, so there is nothing to ask about losing.
func TestEscOnTheUnlockFormAfterAWrongPassphraseClosesAtOnce(t *testing.T) {
	m := unlockSent(t, "wrong-one", "")
	wrong := view.Errorf("kv.wrongpass", "could not decrypt the store").WithInput("passphrase")
	out, _ := m.Update(resultMsg{cap: m.current, err: wrong, seq: m.runSeq})
	back := press(t, out.(Model), "esc")
	if back.dialog != nil || back.mode == modeForm {
		t.Errorf("esc asked about a form holding nothing it would lose (mode %v, dialog %v)", back.mode, back.dialog != nil)
	}
}
