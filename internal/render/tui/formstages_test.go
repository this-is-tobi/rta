package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A form that asks in two stages (kv.set, note.edit: the identity of the record,
// then the record's contents seeded with what it holds) used to forget the first
// stage on screen: the key typed was nowhere on the page the value was typed
// into, and the fast key said submit over a stage it only moved on from.

func stagedCap(keys ...plugin.Field) plugin.Capability {
	c := plugin.Capability{
		ID: "demo.set", Summary: "set a value", Safety: plugin.Write,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		Prefill: func(context.Context, plugin.Request) (map[string]any, error) {
			return map[string]any{"value": "old"}, nil
		},
	}
	c.Inputs = append(c.Inputs, keys...)
	c.Inputs = append(c.Inputs, plugin.Field{Name: "value", Type: plugin.String})
	return c
}

func stageOne(t *testing.T, c plugin.Capability) Model {
	t.Helper()
	m := openFormOn(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), c)
	m.current = c
	m.width, m.height = 100, 30
	m.fitForm()
	if m.form.final {
		t.Fatal("the capability did not open in stages")
	}
	return m
}

func typed(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m = press(t, m, string(r))
	}
	return m
}

func TestTheFastKeySaysNextOverTheFirstStageAndSubmitOverTheLast(t *testing.T) {
	m := stageOne(t, stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true}))
	if bar := plain(m.footerFor(modeForm)); !strings.Contains(bar, "ctrl+s next") || strings.Contains(bar, "submit") {
		t.Errorf("stage one bar = %q, want the fast key to say next", bar)
	}
	second := pressEnter(t, typed(t, m, "mykey"))
	if second.form == nil || !second.form.final {
		t.Fatal("enter did not open the second stage")
	}
	if bar := plain(second.footerFor(modeForm)); !strings.Contains(bar, "ctrl+s submit") {
		t.Errorf("stage two bar = %q, want the fast key to say submit", bar)
	}
}

func TestTheSecondStageSaysWhatTheFirstChose(t *testing.T) {
	m := stageOne(t, stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true}))
	second := pressEnter(t, typed(t, m, "mykey"))
	if head := headOf(second); !strings.Contains(head, "key mykey") {
		t.Errorf("stage two head = %q, want the key chosen on stage one", head)
	}
	if head := headOf(m); strings.Contains(head, "mykey") {
		t.Errorf("stage one head = %q already names the key", head)
	}
}

// What the first stage chose is carried onto the page with the boxes that
// replace it, as the picker rebuilds them.
func TestTheStageOneAnswerStaysWhenTheSecondStageIsRebuilt(t *testing.T) {
	m := stageOne(t, stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true}))
	second := pressEnter(t, typed(t, m, "mykey"))
	back, _ := second.backToRunForm(second.form, "")
	if head := headOf(back.(Model)); !strings.Contains(head, "key mykey") {
		t.Errorf("after a rebuild the head = %q, want the key chosen on stage one", head)
	}
}

// A secret is masked in its box; it is not read back to the person in a title.
func TestTheSecondStageNeverRepeatsASecretTheFirstAsked(t *testing.T) {
	m := stageOne(t, stagedCap(
		plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true},
		plugin.Field{Name: "pass", Type: plugin.Secret, Positional: true, Required: true},
	))
	m = pressEnter(t, typed(t, m, "mykey"))
	second := pressEnter(t, typed(t, m, "hunter2"))
	if second.form == nil || !second.form.final {
		t.Fatal("the two identity boxes did not lead to the second stage")
	}
	head := headOf(second)
	if strings.Contains(head, "hunter2") || strings.Contains(head, "pass") {
		t.Errorf("stage two head = %q repeats the secret", head)
	}
	if !strings.Contains(head, "key mykey") {
		t.Errorf("stage two head = %q, want the key still named", head)
	}
}

// esc on the second stage closed the whole form, so somebody who typed the wrong
// key had to start again from the dashboard. It goes back to the first stage with
// the key in its box, and the bar says so.
func TestEscOnTheSecondStageGoesBackToTheFirst(t *testing.T) {
	m := stageOne(t, stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true}))
	second := pressEnter(t, typed(t, m, "mykey"))
	if bar := plain(second.footerFor(modeForm)); !strings.Contains(bar, "esc back") {
		t.Errorf("stage two bar = %q, want esc to say back", bar)
	}
	first := press(t, second, "esc")
	if first.dialog != nil || first.form == nil || first.mode != modeForm || first.form.final {
		t.Fatalf("esc on an untouched second stage: dialog %v, form %v, mode %v; want the first stage",
			first.dialog != nil, first.form != nil, first.mode)
	}
	if got := boxText(first, "key"); got != "mykey" {
		t.Errorf("the first stage's box reads %q, want the key it was answered", got)
	}
	if bar := plain(first.footerFor(modeForm)); !strings.Contains(bar, "esc cancel") {
		t.Errorf("stage one bar = %q, want esc to say cancel again", bar)
	}
	// The key was typed, so leaving the first stage now asks, as it would have.
	if asked := press(t, first, "esc"); asked.dialog == nil || asked.dialog.id != dialogDiscardForm {
		t.Errorf("esc on the first stage after coming back asked %v, want the discard question", asked.dialog)
	}
}

func TestEscOnASecondStageTypedInAsksBeforeGoingBack(t *testing.T) {
	m := stageOne(t, stagedCap(plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true}))
	second := strike(pressEnter(t, typed(t, m, "mykey")), "x")
	asked := press(t, second, "esc")
	if asked.dialog == nil || asked.dialog.id != dialogDiscardStep {
		t.Fatalf("esc on a second stage typed in asked %v, want the step question", asked.dialog)
	}
	if kept := press(t, asked, "n"); kept.form == nil || !kept.form.final {
		t.Errorf("keep editing left the second stage")
	}
	back := press(t, asked, "y")
	if back.form == nil || back.form.final || boxText(back, "key") != "mykey" {
		t.Errorf("discard did not go back to the first stage with its key")
	}
}

// A secret is the one answer that never reseeds a form: going back asks it again.
func TestGoingBackDoesNotPutASecretBackInItsBox(t *testing.T) {
	m := stageOne(t, stagedCap(
		plugin.Field{Name: "key", Type: plugin.String, Positional: true, Required: true},
		plugin.Field{Name: "pass", Type: plugin.Secret, Positional: true, Required: true},
	))
	m = pressEnter(t, typed(t, m, "mykey"))
	second := pressEnter(t, typed(t, m, "hunter2"))
	first := press(t, second, "esc")
	if first.form == nil || first.form.final {
		t.Fatal("esc did not go back to the first stage")
	}
	if got := boxText(first, "key"); got != "mykey" {
		t.Errorf("key box = %q, want mykey", got)
	}
	if got := boxText(first, "pass"); got != "" {
		t.Errorf("the secret came back in its box: %q", got)
	}
}
