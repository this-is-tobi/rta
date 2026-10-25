package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// makeCap is a write with the boxes a refusal can name: a name it needs, a
// number, a credential and a note.
func makeCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.item.make", Summary: "make an item", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Required: true},
			{Name: "length", Type: plugin.Int, Default: 20},
			{Name: "token", Type: plugin.Secret},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "made"}, nil },
	}
}

// sentWith opens c's form on m, answers its boxes in turn — "" leaves one as it
// opened — and sends it: the model with the run in flight.
func sentWith(t *testing.T, m Model, c plugin.Capability, answers ...string) Model {
	t.Helper()
	m.current = c
	m = openFormOn(t, m, c)
	m.fitForm()
	for _, a := range answers {
		m = pressEnter(t, typed(t, m, a))
	}
	if m.submitted == nil {
		t.Fatalf("the form of %s was not sent: mode %v", c.ID, m.mode)
	}
	return m
}

// refusedWith is m with its run answered by verr.
func refusedWith(t *testing.T, m Model, verr *view.Error) (Model, Model) {
	t.Helper()
	out, cmd := m.Update(resultMsg{cap: m.current, err: verr, seq: m.runSeq})
	got := out.(Model)
	return got, feedBack(t, got, cmd)
}

// A refusal of one of the boxes a form sent is answered on that form: every
// answer back in its box but the credential's, the cursor on the box it names,
// the reason on the bar, and r holding what it held before the form was sent.
func TestARefusalOfOneBoxIsAnsweredOnTheFormThatSentIt(t *testing.T) {
	c := makeCap()
	m := sentWith(t, demoModel(t, c), c, "widget", "7", "s3cret", "hello")
	got, settled := refusedWith(t, m, view.Errorf("demo.item.short", "a widget is longer than 7").
		WithHint("give it 10 or more").WithInput("length"))

	if got.mode != modeForm || got.form == nil || got.form.cap.ID != c.ID {
		t.Fatalf("the refusal left mode %v, want the form again", got.mode)
	}
	for box, want := range map[string]string{"name": "widget", "length": "7", "note": "hello", "token": ""} {
		if have := boxText(got, box); have != want {
			t.Errorf("the %s box holds %q, want %q", box, have, want)
		}
	}
	if at := focusedName(t, settled); at != "length" {
		t.Errorf("the cursor is on %s, want the length box the refusal names", at)
	}
	if bar := plain(got.footerFor(modeForm)); !strings.Contains(bar, "a widget is longer than 7") {
		t.Errorf("the bar does not say why:\n%s", bar)
	}
	if got.lastValues != nil {
		t.Errorf("r holds %v, the run the form sent, want what it held before", got.lastValues)
	}
	// esc on it asks before losing what was typed back into it.
	if asked := press(t, got, "esc"); asked.dialog == nil {
		t.Error("esc dropped the answers the form was opened again on, without asking")
	}
}

// What a form sent is held only until its run answers, since a credential typed
// into it is part of it: an answer of any kind, a success included, takes it off
// the model.
func TestWhatAFormSentGoesWithTheAnswerToIt(t *testing.T) {
	m := unlockSent(t, "right-one", "")
	out, _ := m.Update(resultMsg{cap: m.current, view: view.Text{Body: "store unlocked"}, seq: m.runSeq})
	if out.(Model).submitted != nil {
		t.Error("an unlock that worked kept the passphrase it was sent with")
	}
	c := makeCap()
	m = sentWith(t, demoModel(t, c), c, "widget", "", "s3cret", "")
	out, _ = m.Update(resultMsg{cap: c, err: view.Errorf("demo.item.broke", "the service broke"), seq: m.runSeq})
	if out.(Model).submitted != nil {
		t.Error("a failure kept what the form sent")
	}
}

// Everything else keeps the page: a refusal that names no input, one naming an
// input the capability does not declare, one that may not happen again (`r` on
// the page is for that), and one that was not the answer to a form.
func TestARefusalNamingNoBoxOfTheFormIsAPage(t *testing.T) {
	c := makeCap()
	for name, verr := range map[string]*view.Error{
		"no input":          view.Errorf("demo.item.broke", "the service broke"),
		"an undeclared one": view.Errorf("demo.item.broke", "the service broke").WithInput("ghost"),
		"a retryable one":   {Code: "demo.item.busy", Message: "try again", Input: "length", Retryable: true},
	} {
		m := sentWith(t, demoModel(t, c), c, "widget", "", "", "")
		if got, _ := refusedWith(t, m, verr); got.mode != modeResult {
			t.Errorf("%s: mode %v, want the page", name, got.mode)
		}
	}
	m := sentWith(t, demoModel(t, c), c, "widget", "", "", "")
	out, _ := m.Update(resultMsg{cap: c, err: view.Errorf("demo.item.short", "too short").WithInput("length")})
	if out.(Model).mode != modeResult {
		t.Error("a result no form sent was answered on a form")
	}
}

// A second stage that sent the call is opened again as that stage, the first
// stage's answer still above it, and esc goes back a step as it does there; a
// refusal of the first stage's answer opens the first stage on it.
func TestARefusalOfATwoStageFormOpensTheStageThatHoldsTheBox(t *testing.T) {
	c := stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true})
	second := pressEnter(t, typed(t, stageOne(t, c), "mykey"))
	sent := pressEnter(t, second)
	if sent.submitted == nil {
		t.Fatalf("the second stage was not sent: mode %v", sent.mode)
	}

	got, settled := refusedWith(t, sent, view.Errorf("demo.value.bad", "not a value").WithInput("value"))
	if got.mode != modeForm || !got.form.final || got.form.firstStage == nil {
		t.Fatalf("a refusal of the value opened mode %v, want the second stage again", got.mode)
	}
	if head := headOf(got); !strings.Contains(head, "key mykey") {
		t.Errorf("the head says %q, want the key the first stage chose", head)
	}
	if at := focusedName(t, settled); at != "value" || boxText(got, "value") != "old" {
		t.Errorf("the cursor is on %s holding %q, want the value box", at, boxText(got, "value"))
	}
	if back := press(t, got, "esc"); back.form == nil || back.form.final || boxText(back, "key") != "mykey" {
		t.Error("esc on the second stage opened again did not go back to the first")
	}

	got, settled = refusedWith(t, sent, view.Errorf("demo.key.bad", "no key mykey").WithInput("key"))
	if got.mode != modeForm || got.form.final || boxText(got, "key") != "mykey" {
		t.Fatalf("a refusal of the key opened mode %v, want the first stage on it", got.mode)
	}
	if at := focusedName(t, settled); at != "key" || !strings.Contains(got.flashBad, "no key mykey") {
		t.Errorf("the cursor is on %s and the bar says %q, want the key box and why", at, got.flashBad)
	}
}

// The first stage asks for the record to fetch what the second edits, and a
// record that is not there was a page with the answer gone: it is answered on
// the first stage, the answer in its box.
func TestARecordThatIsNotThereIsAnsweredOnTheFirstStage(t *testing.T) {
	c := stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true})
	c.Prefill = func(context.Context, plugin.Request) (map[string]any, error) {
		return nil, view.Errorf("demo.nokey", "there is no record mykey").WithInput("key")
	}
	got := pressEnter(t, typed(t, stageOne(t, c), "mykey"))
	if got.mode != modeForm || got.form.final || boxText(got, "key") != "mykey" {
		t.Fatalf("a missing record left mode %v, want the first stage with its answer", got.mode)
	}
	if at := focusedName(t, got); at != "key" || !strings.Contains(got.flashBad, "there is no record mykey") {
		t.Errorf("the cursor is on %s and the bar says %q, want the key box and why", at, got.flashBad)
	}
}

// A destructive call's form goes through its confirmation, and a refusal of one
// of its boxes is answered on the form from either side of the question: the
// dry run's, and the run's after enter.
func TestADestructiveCallIsAnsweredOnItsFormBeforeAndAfterTheQuestion(t *testing.T) {
	var runs []bool
	c := wipe(&runs)
	refuse := view.Errorf("demo.wipe.target", "there is no target bad").WithInput("target")

	m := sentWith(t, confirmModel(t, c), c, "bad", "")
	out, _ := m.Update(previewMsg{cap: c, err: refuse, seq: m.runSeq})
	if got := out.(Model); got.mode != modeForm || focusedName(t, got) != "target" {
		t.Errorf("a dry run refusing the target left mode %v, want the form on the target box", got.mode)
	}

	m = sentWith(t, confirmModel(t, c), c, "late", "")
	out, _ = m.Update(previewMsg{cap: c, view: view.Text{Body: "would wipe late"}, seq: m.runSeq})
	asked := out.(Model)
	if asked.mode != modeConfirm {
		t.Fatalf("the preview left mode %v, want the question", asked.mode)
	}
	running := press(t, asked, "enter")
	got, settled := refusedWith(t, running, refuse)
	if got.mode != modeForm || focusedName(t, settled) != "target" || boxText(got, "target") != "late" {
		t.Errorf("the run's refusal after the question left mode %v, want the form on the target box", got.mode)
	}
}

// A refusal on a page no form sent — a tile opened, a quick run, r — opens the
// form e brings up on the box it names, as the form that sent it would have been.
func TestEOnAPageOpensTheFormOnTheBoxItsRefusalNames(t *testing.T) {
	c := makeCap()
	rm := withResultHeld(t, demoModel(t, c), c, nil, view.Errorf("demo.item.note", "too long").WithInput("note"))
	if at := focusedName(t, press(t, rm, "e")); at != "note" {
		t.Errorf("e opened the form on %s, want the note box", at)
	}
}

// A connection's fence is held before anything runs, and its refusal of the
// value a box sent is answered on that box, not on a page.
func TestAValueOutsideTheFenceIsAnsweredOnItsBox(t *testing.T) {
	m := fencedStagingModel(t, []any{"app", "app-jobs"})
	switchOn(t, &m, "staging")
	c := dbPlugin().Capabilities[1]
	m.current = c
	nm, _ := m.startForm(c, map[string]any{"schema": "prod"})
	m = nm.(Model)
	m.form.form = startedForm(m.form)
	out, _ := m.fastSubmitForm()
	got := out.(Model)
	if got.mode != modeForm {
		t.Fatalf("a value outside the fence left mode %v, want the form", got.mode)
	}
	if at := focusedName(t, got); at != "schema" || !strings.Contains(got.flashBad, "app, app-jobs") {
		t.Errorf("the cursor is on %s and the bar says %q, want the schema box and what it reaches", at, got.flashBad)
	}
}
