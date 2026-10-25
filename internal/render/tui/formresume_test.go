package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `e` on a result opens its inputs, and esc on that form is cancelling an edit,
// not closing the result: the result is what the person was looking at, and the
// form is a detour from it. esc used to land on the dashboard with the result
// gone, after a run that had failed as much as after one that had worked.

const heldBody = "THE-HELD-RESULT"

func editableCap(extra ...plugin.Field) plugin.Capability {
	return plugin.Capability{
		ID: "demo.edit", Summary: "s", Safety: plugin.Read,
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: heldBody}, nil },
		Inputs: append([]plugin.Field{{Name: "length", Type: plugin.Int, Default: 20}}, extra...),
	}
}

// demoModel is a model over c alone, on a terminal of an ordinary size.
func demoModel(t *testing.T, c plugin.Capability) Model {
	t.Helper()
	next, _ := New(quickPlugin(t, c), config.Dashboard{}, nil).Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Model)
}

// withResultHeld puts the result of a run of c on m's screen, the way the run's
// own message does.
func withResultHeld(t *testing.T, m Model, c plugin.Capability, v view.View, verr *view.Error) Model {
	t.Helper()
	next, _ := m.Update(resultMsg{cap: c, view: v, err: verr})
	rm := next.(Model)
	if rm.mode != modeResult {
		t.Fatalf("the result left the shell in mode %v, want the result screen", rm.mode)
	}
	return rm
}

func TestEscOnTheFormEOpenedGoesBackToTheResult(t *testing.T) {
	c := editableCap()
	rm := withResultHeld(t, demoModel(t, c), c, view.Text{Body: heldBody}, nil)

	form := press(t, rm, "e")
	if form.mode != modeForm {
		t.Fatalf("e left the shell in mode %v, want the form", form.mode)
	}
	back := press(t, form, "esc")

	if back.mode != modeResult || back.form != nil {
		t.Fatalf("esc left the shell in mode %v (form %v), want the result it came from", back.mode, back.form != nil)
	}
	if body := plain(back.resultView()); !strings.Contains(body, heldBody) {
		t.Errorf("the result is not on screen after esc:\n%s", body)
	}
}

func TestEscOnTheFormEOpenedAfterAFailureKeepsTheError(t *testing.T) {
	c := editableCap()
	failed := withResultHeld(t, demoModel(t, c), c, nil, view.AsError(errors.New("WRONG-PASSPHRASE"), "demo.failed"))

	back := press(t, press(t, failed, "e"), "esc")

	if back.mode != modeResult {
		t.Fatalf("esc left the shell in mode %v, want the error it came from", back.mode)
	}
	if body := plain(back.resultView()); !strings.Contains(body, "WRONG-PASSPHRASE") {
		t.Errorf("the error is not on screen after esc:\n%s", body)
	}
}

// A form opened from anywhere else has no result to go back to: esc closes it
// to where the flow began, a dashboard here, even when an earlier run of the
// same call left its result behind.
func TestEscOnAFormOpenedFromTheDashboardStillClosesToTheDashboard(t *testing.T) {
	c := editableCap(plugin.Field{Name: "name", Type: plugin.String, Required: true})
	m := demoModel(t, c)
	m.result = resultMsg{cap: c, view: view.Text{Body: heldBody}}
	m.origin = modeDashboard

	model, _ := m.startForm(c, nil)
	opened := model.(Model)
	opened.form.form = startedForm(opened.form)

	if back := press(t, opened, "esc"); back.mode != modeDashboard {
		t.Errorf("esc left the shell in mode %v, want the dashboard", back.mode)
	}
}

// The way back survives what a form does to itself: the second stage of a form
// that asks in two, and the picker rebuilding the boxes on another environment.
func TestTheWayBackSurvivesTheFormRebuildingItself(t *testing.T) {
	staged := editableCap()
	staged.Inputs = []plugin.Field{
		{Name: "key", Type: plugin.String, Positional: true, Required: true},
		{Name: "value", Type: plugin.String},
	}
	staged.Prefill = func(context.Context, plugin.Request) (map[string]any, error) {
		return map[string]any{"value": "old"}, nil
	}
	rm := withResultHeld(t, demoModel(t, staged), staged, view.Text{Body: heldBody}, nil)
	rm.lastValues = map[string]any{"key": "k"}

	first := press(t, rm, "e")
	if first.form == nil || first.form.final {
		t.Fatalf("e did not open the first of two stages (form %v)", first.form != nil)
	}
	second := pressEnter(t, first)
	if second.form == nil || !second.form.final {
		t.Fatalf("enter did not open the second stage (form %v)", second.form != nil)
	}
	// esc on the second stage goes back to the first, and from there to the
	// result the form was opened from.
	first = press(t, second, "esc")
	if first.form == nil || first.form.final {
		t.Fatalf("esc on the second stage did not go back to the first (form %v)", first.form != nil)
	}
	if back := press(t, first, "esc"); back.mode != modeResult {
		t.Errorf("esc on the first stage left mode %v, want the result", back.mode)
	}

	env := referencedModel(t)
	switchOn(t, &env, "staging")
	vlt := vltCap(t, env)
	env.current = vlt
	env.result = resultMsg{cap: vlt, view: view.Text{Body: heldBody}}
	env.mode = modeResult
	env.renderResult()
	opened := press(t, env, "e")
	if opened.form == nil {
		t.Fatalf("e on the environment's result opened no form")
	}
	*opened.form.bindings[profileInput] = "prod"
	moved, _, rebuilt := opened.reseedOnPickerMove()
	if !rebuilt {
		t.Fatal("moving the picker rebuilt nothing")
	}
	if back := press(t, moved.(Model), "esc"); back.mode != modeResult {
		t.Errorf("esc after the picker moved left mode %v, want the result", back.mode)
	}
}

// And through the detour that states a connection for the call, whose way back
// is the run form it was opened from.
func TestTheWayBackSurvivesTheAdHocDetour(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	c := capNamed(t, m, "pgx.status")
	m.current = c
	m.result = resultMsg{cap: c, view: view.Text{Body: heldBody}}
	m.mode = modeResult
	m.renderResult()

	opened := press(t, m, "e")
	if opened.form == nil {
		t.Fatal("e opened no form")
	}
	opened.form.form = startedForm(opened.form)
	detour := pickAdHoc(t, opened)
	if detour.form == nil || detour.form.adHocFrom == nil {
		t.Fatal("the picker did not open the ad hoc form")
	}
	back, _ := detour.leaveAdHocForm()
	runForm := back.(Model)
	if runForm.mode != modeForm || runForm.form == nil || runForm.form.adHocFrom != nil {
		t.Fatalf("leaving the ad hoc form did not return to the run form (mode %v)", runForm.mode)
	}
	if got := press(t, runForm, "esc"); got.mode != modeResult {
		t.Errorf("esc on the run form left mode %v, want the result", got.mode)
	}
}

// A live result refreshes itself in place, and a form over it stops that. Back
// on the result, it is live again.
func TestAResultBroughtBackKeepsRefreshing(t *testing.T) {
	c := editableCap()
	c.Live = true
	rm := withResultHeld(t, demoModel(t, c), c, view.Text{Body: heldBody}, nil)
	form := press(t, rm, "e")
	gen := form.tickGen

	next, cmd := form.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	back := next.(Model)

	if back.mode != modeResult || back.tickGen == gen || cmd == nil {
		t.Errorf("mode %v, tickGen %d (was %d), cmd %v: want the result with a refresh armed", back.mode, back.tickGen, gen, cmd != nil)
	}
}
