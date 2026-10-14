package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// enterWordOf is the word the footer gives enter, the entry that reads "enter
// <word>" in the form's bar.
func enterWordOf(t *testing.T, m Model) string {
	t.Helper()
	bar := plain(m.footerFor(modeForm))
	i := strings.Index(bar, "enter ")
	if i < 0 {
		t.Fatalf("the form's footer does not name enter:\n%s", bar)
	}
	rest := bar[i+len("enter "):]
	if end := strings.IndexAny(rest, " ·\n"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// pressEnter is enter on the form, with the field move it asks the event loop
// for fed back in the way settleForm does, and what the model does once the
// form has completed.
func pressEnter(t *testing.T, m Model) Model {
	t.Helper()
	m.form.form = settleForm(m.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	out, _ := m.afterFormUpdate(nil)
	return out.(Model)
}

// The footer said "enter next" on the last field of every form, with the ways
// to submit listed beside it under other keys, and enter on that field ran the
// call or saved the grant. Enter is named for what it does where the cursor is.
func TestEnterIsNamedForWhatItDoesOnTheLastField(t *testing.T) {
	put := plugin.Capability{
		ID: "demo.put", Summary: "write a value", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Default: "a"},
			{Name: "value", Type: plugin.String, Default: "b"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "put"}, nil },
	}
	m := openFormOn(t, New(fastFormRegistry(t, put), config.Dashboard{}, nil), put)

	if got := enterWordOf(t, m); got != "next" {
		t.Errorf("on the first of two fields enter is %q, want next", got)
	}
	m = pressEnter(t, m)
	if got := enterWordOf(t, m); got != "run" {
		t.Errorf("on the last field of a write enter is %q, want run", got)
	}
	if m = pressEnter(t, m); m.mode == modeForm {
		t.Error("enter on the field the footer called run did not leave the form")
	}
}

// A destructive capability's last field leads to the screen that shows what the
// call would do and takes the consent, not to the call.
func TestEnterOnTheLastFieldOfADestructiveFormSaysItOpensThePreview(t *testing.T) {
	drop := plugin.Capability{
		ID: "demo.drop", Summary: "drop a value", Safety: plugin.Destructive,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Default: "a"},
			{Name: "value", Type: plugin.String, Default: "b"},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "dropped"}, nil },
	}
	m := openFormOn(t, New(fastFormRegistry(t, drop), config.Dashboard{}, nil), drop)
	// What open sets before it asks for the form, and what completing it reads.
	m.current = drop
	m = pressEnter(t, m)
	if got := enterWordOf(t, m); got != "preview" {
		t.Errorf("on the last field of a destructive form enter is %q, want preview", got)
	}
	// What enter does there is the dry run, which is the screen's own content:
	// a real run would have produced no preview at all.
	m.form.form = settleForm(m.form.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	_, cmd := m.afterFormUpdate(nil)
	previewOf(t, cmd)
}
