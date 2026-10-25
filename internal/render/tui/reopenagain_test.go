package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// tokenCap is codec.jwt's shape: a required credential, then one that decides
// what the call does when it is left out, around the boxes a refusal can name.
func tokenCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.token.check", Summary: "check a token", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Required: true},
			{Name: "token", Type: plugin.Secret, Required: true},
			{Name: "key", Type: plugin.Secret},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "checked"}, nil },
	}
}

// A credential the call was sent with is not in the form built again, and the
// walk to the refused box validates nothing it passes, so a required one
// emptied before that box went out empty: the cursor goes to it first, the bar
// says it was cleared, and the fast key cannot send the form without it.
func TestACredentialTheFormCameBackWithoutIsAskedBeforeTheRefusedBox(t *testing.T) {
	c := tokenCap()
	m := sentWith(t, demoModel(t, c), c, "widget", "tok", "k1", "hello")
	got, settled := refusedWith(t, m, view.Errorf("demo.token.key", "that key checks no signature").WithInput("key"))
	if got.mode != modeForm {
		t.Fatalf("the refusal left mode %v, want the form again", got.mode)
	}
	if at := focusedName(t, settled); at != "token" {
		t.Errorf("the cursor is on %s, want the token box the form came back without", at)
	}
	if !strings.Contains(got.flashBad, "the token box was cleared") || !strings.Contains(got.flashBad, "checks no signature") {
		t.Errorf("the bar says %q, want the refusal and the box that was cleared", got.flashBad)
	}
	out, _ := settled.fastSubmitForm()
	if still := out.(Model); still.mode != modeForm || still.submitted != nil {
		t.Errorf("the fast key sent the form without the token (mode %v)", still.mode)
	}
}

// An empty credential box can do something weaker than the call it was typed
// for — a key written with no passphrase — so one the form came back without
// refuses to be left empty after the refused box too.
func TestAnEmptiedCredentialAfterTheRefusedBoxStopsTheFastKey(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.key.add", Summary: "add a key", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "out", Type: plugin.String, Required: true},
			{Name: "passphrase", Type: plugin.Secret},
			{Name: "comment", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "added"}, nil },
	}
	m := sentWith(t, demoModel(t, c), c, "/tmp/id_work", "pp", "me")
	got, settled := refusedWith(t, m, view.Errorf("demo.key.exists", "/tmp/id_work already exists").WithInput("out"))
	if at := focusedName(t, settled); at != "out" || !strings.Contains(got.flashBad, "the passphrase box was cleared") {
		t.Fatalf("the cursor is on %s and the bar says %q, want the out box and the cleared passphrase", at, got.flashBad)
	}
	out, _ := settled.fastSubmitForm()
	still := out.(Model)
	if still.mode != modeForm || still.submitted != nil {
		t.Fatalf("the fast key sent the form with the passphrase it came back without (mode %v)", still.mode)
	}
	if at := focusedName(t, still); at != "passphrase" {
		t.Errorf("the fast key stopped on %s, want the passphrase box", at)
	}
}

// A refusal of the key sends a form that asks in two back to its first stage,
// and what the second stage was answered with comes back with it once the key
// is answered again, laid over what the record holds.
func TestARefusedKeyKeepsWhatTheSecondStageWasAnswered(t *testing.T) {
	c := stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true})
	second := typed(t, pressEnter(t, typed(t, stageOne(t, c), "mykey")), "X")
	sent := pressEnter(t, second)
	got, _ := refusedWith(t, sent, view.Errorf("demo.key.bad", "no key mykey").WithInput("key"))
	if got.form == nil || got.form.final {
		t.Fatalf("a refusal of the key left mode %v, want the first stage", got.mode)
	}
	again := pressEnter(t, typed(t, got, "2"))
	if again.form == nil || !again.form.final {
		t.Fatal("the first stage answered again did not open the second")
	}
	if box := boxText(again, "value"); box != "oldX" {
		t.Errorf("the value box holds %q, want the oldX it was answered with", box)
	}
	if !again.form.stepTouched() {
		t.Error("going back from the second stage would drop the answers carried back without asking")
	}
}

// The record of what a form sent goes with any other run, and with every way
// off the screens that wait for its own: a credential typed into it is part of
// it.
func TestTheRecordOfASentFormGoesWithAnyOtherRunAndEveryCancel(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	asked := func(t *testing.T) Model {
		t.Helper()
		m := sentWith(t, confirmModel(t, c), c, "late", "tok-typed")
		out, _ := m.Update(previewMsg{cap: c, view: view.Text{Body: "would wipe late"}, seq: m.runSeq})
		got := out.(Model)
		if got.mode != modeConfirm || got.submitted == nil {
			t.Fatalf("the preview left mode %v with no record, want the question", got.mode)
		}
		return got
	}
	other := asked(t)
	_ = other.startRun(plugin.Capability{ID: "demo.other", Run: c.Run}, nil, false)
	if other.submitted != nil {
		t.Error("another run kept what the form sent")
	}
	for _, key := range []string{"esc", "e"} {
		if left := press(t, asked(t), key); left.submitted != nil {
			t.Errorf("%s on the question kept what the form sent", key)
		}
	}
	w := makeCap()
	running := sentWith(t, demoModel(t, w), w, "widget", "", "s3cret", "")
	if left := press(t, running, "esc"); left.submitted != nil {
		t.Error("esc on the running screen kept what the form sent")
	}
}

// A confirmation with nothing to preview runs nothing, and it is a screen of
// its own all the same: what was in flight is cancelled, so its late answer
// can neither replace the question nor be taken for the answer to the form.
func TestAConfirmationWithNoPreviewIsAScreenOfItsOwn(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	m := confirmModel(t, c)
	bare := c
	bare.Run = nil
	m.runSeq = 7
	_ = m.startConfirm(bare, map[string]any{"target": "late"})
	if m.runSeq != 8 || m.result.seq != 8 || m.mode != modeConfirm {
		t.Fatalf("seq %d (result %d), mode %v, want a question of its own at 8", m.runSeq, m.result.seq, m.mode)
	}
	out, _ := m.Update(resultMsg{cap: plugin.Capability{ID: "demo.list"},
		err: view.Errorf("demo.list.gone", "no such target").WithInput("target"), seq: 7})
	if out.(Model).mode != modeConfirm {
		t.Error("an answer to the run before the question replaced it")
	}
}

// e opens the form on the box the page's refusal names and leaves the cursor
// there once the event loop has run what it asked for: firstFocus's moves are
// not put on top of the walk, which a form whose first box to ask is not its
// first would show.
func TestEOnAPageLeavesTheCursorWhereTheRefusalPutIt(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.item.tune", Summary: "tune an item", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "label", Type: plugin.String},
			{Name: "name", Type: plugin.String, Required: true},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "tuned"}, nil },
	}
	rm := withResultHeld(t, demoModel(t, c), c, nil, view.Errorf("demo.item.note", "too long").WithInput("note"))
	out, cmd := rm.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	got := feedBack(t, out.(Model), cmd)
	if got.mode != modeForm || focusedName(t, got) != "note" {
		t.Errorf("e left mode %v on %s, want the form on the note box", got.mode, focusedName(t, got))
	}
}
