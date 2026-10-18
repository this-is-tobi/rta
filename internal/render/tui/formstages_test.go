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
