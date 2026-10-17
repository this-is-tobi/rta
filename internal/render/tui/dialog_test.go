package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/builtin/kv"
)

// The dialog tests are about the component as a person meets it: the keys a web
// dialog answers to, that nothing underneath hears anything while it is up, and
// that it fits the terminal it is drawn on. The question U asks is the first
// user; the answers themselves are the switch in answerDialog.
//
// There are two ways to have the question open, and the price of each is why
// they are two. A held store costs a key derivation to create and another to
// open, which the race detector multiplies by an order of magnitude, so only the
// test whose subject is the store (the first one) pays for it, and it asks as
// many times as it likes on the one session. Everything about the component
// itself opens the question directly on a screen with no store behind it, which
// is also the state of a sitting that ended while the question was open: a yes
// then finds nothing to lock and says so, and a no says the store is still
// open, so the two answers can be told apart without a store.

func unlockedModel(t *testing.T) Model {
	t.Helper()
	m, _ := kvModel(t)
	lockedStore(t, m)
	if verr := kv.StartSession(unlockPassphrase, time.Hour); verr != nil {
		t.Fatal(verr)
	}
	t.Cleanup(kv.ForgetSession)
	m = filled(t, New(m.reg, m.dash, nil), 100, 30)
	return m
}

func pressU(t *testing.T, m Model) Model {
	t.Helper()
	next, _, _ := m.dashboardKeys(keyMsg("U"))
	got := next.(Model)
	if got.dialog == nil {
		t.Fatal("U did not ask")
	}
	return got
}

// questionOpen is the question open over a dashboard with no store behind it.
func questionOpen(t *testing.T) Model {
	t.Helper()
	kv.ForgetSession()
	t.Cleanup(kv.ForgetSession)
	m, _ := realModel(t, 100, 30)
	return m.openDialog(lockStoreDialog(40 * time.Minute))
}

func sent(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	out, _ := m.Update(msg)
	return out.(Model)
}

func stillHeld() bool {
	_, ok := kv.SessionUntil()
	return ok
}

// saidYes and saidNo read an answer off a model that has no store: the lock
// found nothing to lock, or the question was declined.
func saidYes(m Model) bool {
	return m.dialog == nil && strings.Contains(m.flash, "already locked itself")
}
func saidNo(m Model) bool { return m.dialog == nil && strings.Contains(m.flash, "still unlocked") }

func TestOnlyAnAnswerChangesTheStoreAndNoAnswerBeforeItDoes(t *testing.T) {
	m := pressU(t, unlockedModel(t))
	if !stillHeld() {
		t.Fatal("U locked the store before it was answered")
	}
	if m.dialog.focus != focusConfirm {
		t.Errorf("the dialog opens on %d, want the lock itself: locking is the fail-safe direction", m.dialog.focus)
	}
	if !strings.Contains(m.dialog.body, "left") {
		t.Errorf("the question does not say how long the sitting has left: %q", m.dialog.body)
	}

	cancelled := sent(t, m, keyMsg("esc"))
	if !stillHeld() || cancelled.dialog != nil || !strings.Contains(cancelled.flash, "still unlocked") {
		t.Errorf("esc: held=%v dialog=%v flash=%q, want it cancelled", stillHeld(), cancelled.dialog != nil, cancelled.flash)
	}

	again := pressU(t, cancelled)
	onCancel := sent(t, again, tea.KeyPressMsg{Code: tea.KeyTab})
	if onCancel.dialog.focus != focusCancel {
		t.Fatalf("tab left focus on %d, want Cancel", onCancel.dialog.focus)
	}
	declined := sent(t, onCancel, keyMsg("enter"))
	if !stillHeld() || declined.dialog != nil || !strings.Contains(declined.flash, "still unlocked") {
		t.Errorf("enter on Cancel: held=%v dialog=%v flash=%q, want it left open", stillHeld(), declined.dialog != nil, declined.flash)
	}

	locked := sent(t, pressU(t, declined), keyMsg("enter"))
	if stillHeld() || locked.dialog != nil || !strings.Contains(locked.flash, "store locked") {
		t.Errorf("enter on Lock store: held=%v dialog=%v flash=%q, want it locked", stillHeld(), locked.dialog != nil, locked.flash)
	}
}

var space = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}

func TestEnterSpaceAndYTakeTheLockAndEscAndNDoNot(t *testing.T) {
	for name, msg := range map[string]tea.KeyPressMsg{"enter": keyMsg("enter"), "space": space, "y": keyMsg("y"), "Y": keyMsg("Y")} {
		if got := sent(t, questionOpen(t), msg); !saidYes(got) {
			t.Errorf("%s: dialog=%v flash=%q, want it taken as a yes", name, got.dialog != nil, got.flash)
		}
	}
	for name, msg := range map[string]tea.KeyPressMsg{"esc": keyMsg("esc"), "n": keyMsg("n"), "N": keyMsg("N")} {
		if got := sent(t, questionOpen(t), msg); !saidNo(got) {
			t.Errorf("%s: dialog=%v flash=%q, want it taken as a no", name, got.dialog != nil, got.flash)
		}
	}
}

func TestEnterAndSpaceFollowTheFocusAndYAndNDoNot(t *testing.T) {
	onCancel := sent(t, questionOpen(t), tea.KeyPressMsg{Code: tea.KeyTab})
	for name, msg := range map[string]tea.KeyPressMsg{"enter": keyMsg("enter"), "space": space} {
		if got := sent(t, onCancel, msg); !saidNo(got) {
			t.Errorf("%s with the focus on Cancel: dialog=%v flash=%q, want a no", name, got.dialog != nil, got.flash)
		}
	}
	if got := sent(t, onCancel, keyMsg("y")); !saidYes(got) {
		t.Errorf("y with the focus on Cancel: dialog=%v flash=%q, want the lock all the same", got.dialog != nil, got.flash)
	}
}

func TestTheFocusMovesWithTabTheArrowsAndShiftTab(t *testing.T) {
	for name, msg := range map[string]tea.KeyPressMsg{
		"tab":       {Code: tea.KeyTab},
		"right":     {Code: tea.KeyRight},
		"left":      {Code: tea.KeyLeft},
		"shift+tab": {Code: tea.KeyTab, Mod: tea.ModShift},
		"l":         {Code: 'l', Text: "l"},
		"h":         {Code: 'h', Text: "h"},
	} {
		m := sent(t, questionOpen(t), msg)
		if m.dialog == nil || m.dialog.focus != focusCancel {
			t.Errorf("%s left focus on %v, want Cancel", name, m.dialog)
			continue
		}
		if back := sent(t, m, msg); back.dialog.focus != focusConfirm {
			t.Errorf("%s again left focus on %d, want it back on the lock", name, back.dialog.focus)
		}
	}
}

// A web dialog does not close because a key was pressed, and one that did would
// cancel an answer the person had not given.
func TestAStrayKeyDoesNothingAndTheDialogStaysUp(t *testing.T) {
	m := questionOpen(t)
	before := m
	for _, key := range []string{"q", "H", "?", "w", "U", "j", "p", "V", "x", "/"} {
		m = sent(t, m, keyMsg(key))
		if m.dialog == nil || m.dialog.focus != before.dialog.focus || m.mode != before.mode || m.searchEditing || m.query != "" {
			t.Fatalf("%q reached the screen under the dialog: dialog=%v mode=%v editing=%v query=%q", key, m.dialog, m.mode, m.searchEditing, m.query)
		}
		if m.flash != before.flash {
			t.Fatalf("%q answered the question: flash=%q", key, m.flash)
		}
	}
	if _, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}); cmd == nil {
		t.Error("ctrl+c did not quit from under a dialog: the key that leaves the program must not become a no")
	}
}

func TestAWheelTurnAndAPasteDoNothingUnderTheDialog(t *testing.T) {
	m := questionOpen(t)
	m.scroll = 1
	if got := sent(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown}); got.scroll != 1 {
		t.Errorf("the wheel scrolled the screen under the dialog to %d", got.scroll)
	}
	got := sent(t, m, tea.PasteMsg{Content: "gen.password"})
	if got.query != "" || got.searchEditing || got.dialog == nil {
		t.Errorf("a paste reached the search under the dialog: editing=%v query=%q", got.searchEditing, got.query)
	}
}

func TestASittingThatEndsWhileTheQuestionIsOpenIsSaidAndIsNotAnError(t *testing.T) {
	got := sent(t, questionOpen(t), keyMsg("enter"))
	if got.dialog != nil || !strings.Contains(got.flash, "already locked") {
		t.Errorf("dialog=%v flash=%q, want the lapse said", got.dialog != nil, got.flash)
	}
}

func TestAClickOnAButtonAnswersAndAClickAnywhereElseDoesNot(t *testing.T) {
	m := questionOpen(t)
	g := m.dialogGeometry()
	for name, click := range map[string]tea.MouseClickMsg{
		"backdrop": {Button: tea.MouseLeft, X: 0, Y: 0},
		"body":     {Button: tea.MouseLeft, X: g.x + 4, Y: g.y + 2},
		"right":    {Button: tea.MouseRight, X: g.confirm[0], Y: g.buttonRow},
	} {
		if got := sent(t, m, click); got.dialog == nil || got.flash != m.flash {
			t.Errorf("a click on the %s answered the dialog", name)
		}
	}
	if got := sent(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: g.cancel[0] + 1, Y: g.buttonRow}); !saidNo(got) {
		t.Errorf("a click on Cancel: dialog=%v flash=%q, want it closed with the store left open", got.dialog != nil, got.flash)
	}
	if got := sent(t, m, tea.MouseClickMsg{Button: tea.MouseLeft, X: g.confirm[0] + 1, Y: g.buttonRow}); !saidYes(got) {
		t.Errorf("a click on Lock store: dialog=%v flash=%q, want the lock taken", got.dialog != nil, got.flash)
	}
}

func TestTheDialogFitsEveryTerminalAndAlwaysShowsItsButtons(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 16}, {44, 14}} {
		m := sent(t, questionOpen(t), tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		frame := ansi.Strip(m.View().Content)
		lines := strings.Split(frame, "\n")
		if len(lines) > size[1] {
			t.Errorf("%dx%d: the frame is %d lines tall", size[0], size[1], len(lines))
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
		for _, want := range []string{"Lock the store?", "Lock store", "Cancel"} {
			if !strings.Contains(frame, want) {
				t.Errorf("%dx%d: the frame lacks %q:\n%s", size[0], size[1], want, frame)
			}
		}
	}
}

func TestWithoutColourTheFocusedButtonIsBracketed(t *testing.T) {
	m := questionOpen(t)
	m.noColor = true
	frame := ansi.Strip(m.View().Content)
	if !strings.Contains(frame, "[ Lock store ]") {
		t.Errorf("the focused button is not bracketed without colour:\n%s", frame)
	}
	if strings.Contains(frame, "[ Cancel ]") {
		t.Errorf("the other button is bracketed too, so the focus cannot be told:\n%s", frame)
	}
}

func TestTheBarWhileAskedOffersOnlyWhatTheDialogAnswers(t *testing.T) {
	m := questionOpen(t)
	bar := plainFooter(m)
	for _, want := range []string{"enter lock store", "n/esc cancel", "tab switch", "ctrl+c quit"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar lacks %q:\n%s", want, bar)
		}
	}
	for _, gone := range []string{"plugins", "details", "browse", "? help"} {
		if strings.Contains(bar, gone) {
			t.Errorf("the bar still offers %q under a dialog:\n%s", gone, bar)
		}
	}
	m = sent(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if bar := plainFooter(m); !strings.Contains(bar, "enter cancel") || !strings.Contains(bar, "y lock store") {
		t.Errorf("with the focus on Cancel, enter says no and y is the way to the lock:\n%s", bar)
	}
}

func TestUWithNoSessionStillOpensTheUnlockFormAndNeverAsks(t *testing.T) {
	m, _ := kvModel(t)
	lockedStore(t, m)
	next, _, _ := m.dashboardKeys(keyMsg("U"))
	if got := next.(Model); got.dialog != nil || got.mode != modeForm {
		t.Errorf("U on a locked store: dialog=%v mode=%v, want the form, which is the consent", got.dialog != nil, got.mode)
	}
}
