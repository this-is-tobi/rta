package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A default is shown as text in the box, and typing into a box with text in it
// adds to the text: a timeout showing 5 became 59, and `now` in a time box
// became `nowEurope/Paris`. A box that still holds exactly what the form showed
// it, a default and not an answer, takes the first character typed as the
// answer; every other key edits it, and once it has been touched it is a box
// like any other.

func retypeCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.retype", Summary: "a write with defaults", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "limit", Type: plugin.Int, Default: 50},
			{Name: "when", Type: plugin.String, Default: "now"},
			{Name: "note", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
}

// retypeForm is the form of retypeCap opened on what a previous run was given.
func retypeForm(t *testing.T, prev map[string]any) Model {
	t.Helper()
	noHistory(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	c := retypeCap()
	model, _ := New(fastFormRegistry(t, c), config.Dashboard{}, nil).startForm(c, prev)
	m := model.(Model)
	m.form.form = startedForm(m.form)
	m.current = c
	return m
}

func boxText(m Model, name string) string { return *m.form.bindings[name] }

func keyed(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestTheFirstKeyInABoxShowingItsDefaultReplacesIt(t *testing.T) {
	m := strike(retypeForm(t, nil), "9")
	if got := boxText(m, "limit"); got != "9" {
		t.Fatalf("a 9 typed over the default 50 reads %q, want 9", got)
	}
	m = strike(m, "5")
	if got := boxText(m, "limit"); got != "95" {
		t.Errorf("a second key reads %q, want 95: only the first replaces", got)
	}

	m = strike(pressEnter(t, m), "Europe/Paris")
	if got := boxText(m, "when"); got != "Europe/Paris" {
		t.Errorf("a zone typed over `now` reads %q", got)
	}
	if got := m.form.values(); got["limit"] != 95 || got["when"] != "Europe/Paris" {
		t.Errorf("values = %v, want what was typed", got)
	}
}

// What was typed is an answer even when it equals the default, and the next key
// adds to it: a box does not take a second first key because it reads like the
// default again.
func TestATypedValueThatEqualsTheDefaultIsNotReplacedAgain(t *testing.T) {
	m := strike(retypeForm(t, nil), "50")
	if got := boxText(m, "limit"); got != "50" {
		t.Fatalf("limit = %q after typing 50", got)
	}
	m = strike(m, "1")
	if got := boxText(m, "limit"); got != "501" {
		t.Errorf("limit = %q, want 501: the box was typed in and reads like its default", got)
	}
}

func TestAnyOtherKeyEditsTheDefaultAsItAlwaysDid(t *testing.T) {
	cases := map[string]struct {
		key  rune
		want string
	}{
		"backspace": {tea.KeyBackspace, "57"},
		"left":      {tea.KeyLeft, "570"},
		"home":      {tea.KeyHome, "750"},
		"end":       {tea.KeyEnd, "507"},
		"delete":    {tea.KeyDelete, "507"},
	}
	for name, c := range cases {
		m := tap(retypeForm(t, nil), keyed(c.key))
		m = strike(m, "7")
		if got := boxText(m, "limit"); got != c.want {
			t.Errorf("%s then 7 reads %q, want %q", name, got, c.want)
		}
	}
}

func TestPastingIntoABoxShowingItsDefaultAddsToIt(t *testing.T) {
	m := retypeForm(t, nil)
	m = tap(m, tea.PasteMsg{Content: "9"})
	if got := boxText(m, "limit"); got != "509" {
		t.Errorf("a pasted 9 reads %q, want 509", got)
	}
	m = strike(m, "1")
	if got := boxText(m, "limit"); got != "5091" {
		t.Errorf("a key after a paste reads %q, want 5091: the box was edited", got)
	}
}

// An answer somebody gave, now or in the run `e` reopens, is theirs to edit.
func TestAValueSomebodyGaveIsEditedAndNotReplaced(t *testing.T) {
	m := strike(retypeForm(t, map[string]any{"limit": 7}), "9")
	if got := boxText(m, "limit"); got != "79" {
		t.Errorf("a 9 typed after the 7 the last run was given reads %q, want 79", got)
	}
}

// Moving through a box without editing it uses nothing up: it is still the
// default when the cursor comes back.
func TestPassingThroughABoxLeavesItsDefaultToBeReplaced(t *testing.T) {
	m := retypeForm(t, nil)
	m = pressEnter(t, m)
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}) // the move is a command, which has to run
	if got := focusedName(t, m); got != "limit" {
		t.Fatalf("shift+tab landed on %q, want limit", got)
	}
	m = strike(m, "9")
	if got := boxText(m, "limit"); got != "9" {
		t.Errorf("a key typed on coming back reads %q, want 9", got)
	}
}

// A box showing its default still answers nothing, as it always did: the call
// takes the default from the layer it belongs to.
func TestADefaultNobodyTypedOverStillAnswersNothing(t *testing.T) {
	m := retypeForm(t, nil)
	if got := m.form.values(); len(got) != 0 {
		t.Errorf("an untouched form answers %v, want nothing", got)
	}
	m = strike(m, "9")
	if got := m.form.values(); got["limit"] != 9 {
		t.Errorf("values = %v, want limit 9", got)
	}
}
