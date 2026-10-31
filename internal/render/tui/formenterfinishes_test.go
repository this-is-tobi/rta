package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// putCap is a call with the shape of most run forms: boxes it has to have
// answered, and boxes a configuration fills or nobody needs.
func putCap(safety plugin.Safety, inputs ...plugin.Field) plugin.Capability {
	return plugin.Capability{
		ID: "demo.put", Summary: "write a value", Safety: safety, Inputs: inputs,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "put"}, nil },
	}
}

// openPut opens c's run form. Keys that move the cursor go through keyThrough,
// which feeds the form's own moves back as the event loop would.
func openPut(t *testing.T, c plugin.Capability) Model {
	t.Helper()
	m := openFormOn(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), c)
	// What open sets before it asks for the form, and what completing it reads.
	m.current = c
	return m
}

// A form whose one required box is the first it asks was a box and then a
// press of enter per box below it, or a key nobody guessed. Answered, the box
// finishes the form on enter, and the footer says so before the key is pressed.
func TestEnterOnTheOneRequiredBoxFinishesTheForm(t *testing.T) {
	m := openPut(t, putCap(plugin.Write,
		plugin.Field{Name: "name", Type: plugin.String, Required: true},
		plugin.Field{Name: "ttl", Type: plugin.String, Default: "1h"},
		plugin.Field{Name: "note", Type: plugin.String},
	))
	if got := enterWordOf(t, m); got != "next" {
		t.Errorf("on the required box still empty enter is %q, want next", got)
	}
	m = typed(t, m, "a")
	if got := enterWordOf(t, m); got != "run" {
		t.Errorf("on the required box once answered enter is %q, want run", got)
	}
	if m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); m.mode == modeForm {
		t.Error("enter on the one required box, answered, did not finish the form")
	}
}

// Pressing enter down a form to read what it asks is what people do, so a box
// the form does not need keeps enter as the move to the next one, every
// required box answered or not, until the last.
func TestEnterOnABoxTheFormDoesNotNeedStillMovesOn(t *testing.T) {
	m := openPut(t, putCap(plugin.Write,
		plugin.Field{Name: "name", Type: plugin.String, Required: true},
		plugin.Field{Name: "ttl", Type: plugin.String, Default: "1h"},
		plugin.Field{Name: "note", Type: plugin.String},
	))
	m = typed(t, m, "a")
	// tab with nothing to complete is the move to the next box.
	m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := enterWordOf(t, m); got != "next" {
		t.Errorf("on an optional box enter is %q, want next", got)
	}
	if m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); m.mode != modeForm {
		t.Fatal("enter on an optional box finished the form")
	}
	if got := enterWordOf(t, m); got != "run" {
		t.Errorf("on the last box enter is %q, want run", got)
	}
}

// Two boxes to answer: the first one answered moves on, since the form is not
// finished; the second one finishes it.
func TestEnterFinishesOnlyOnceEveryRequiredBoxIsAnswered(t *testing.T) {
	m := openPut(t, putCap(plugin.Write,
		plugin.Field{Name: "name", Type: plugin.String, Required: true},
		plugin.Field{Name: "value", Type: plugin.String, Required: true},
		plugin.Field{Name: "note", Type: plugin.String},
	))
	m = typed(t, m, "a")
	if got := enterWordOf(t, m); got != "next" {
		t.Errorf("with a required box still empty enter is %q, want next", got)
	}
	if m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); m.mode != modeForm {
		t.Fatal("enter left the form with a required box unanswered")
	}
	m = typed(t, m, "b")
	if got := enterWordOf(t, m); got != "run" {
		t.Errorf("on the last required box answered enter is %q, want run", got)
	}
	if m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); m.mode == modeForm {
		t.Error("enter on the last required box, answered, did not finish the form")
	}
}

// A destructive call finishes on the screen that shows what it would do, the
// way its last box always led there: enter never runs one.
func TestEnterOnTheRequiredBoxOfADestructiveFormOpensThePreview(t *testing.T) {
	m := openPut(t, putCap(plugin.Destructive,
		plugin.Field{Name: "name", Type: plugin.String, Required: true},
		plugin.Field{Name: "note", Type: plugin.String},
	))
	m = typed(t, m, "a")
	if got := enterWordOf(t, m); got != "preview" {
		t.Errorf("on the answered required box of a destructive form enter is %q, want preview", got)
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	previewOf(t, cmd)
}

// What the box holds is checked as the form's own enter checks it: a value the
// box cannot take keeps the form open on it, with the reason, and runs nothing.
func TestEnterOnARequiredBoxHoldingWhatItCannotTakeStaysOnIt(t *testing.T) {
	m := openPut(t, putCap(plugin.Write,
		plugin.Field{Name: "count", Type: plugin.Int, Required: true},
		plugin.Field{Name: "note", Type: plugin.String},
	))
	m = typed(t, m, "x")
	if m = keyThrough(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}); m.mode != modeForm {
		t.Fatal("enter ran the form with a count of x")
	}
	if err := m.form.form.GetFocusedField().Error(); err == nil {
		t.Error("the box holding x shows no reason")
	}
}
