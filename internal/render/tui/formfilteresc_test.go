package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A picker's filter is a small popup inside the form, and esc closes a popup
// before it closes the dialog around it. It closed the form instead: a name
// typed in the first box was gone with the screen, because formKeys took esc
// before the picker saw it.

func pickerForm(t *testing.T) Model {
	t.Helper()
	c := plugin.Capability{
		ID: "demo.pick", Summary: "pick things", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String},
			{Name: "ttl", Type: plugin.String, Options: []string{"1h", "12h", "24h", "7d"}, Default: "1h"},
			{Name: "kinds", Type: plugin.StringSlice, Options: []string{"table", "view", "index", "type"}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
	m := openFormOn(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), c)
	m.width, m.height = 100, 30
	m.fitForm()
	for _, r := range "keep-me" {
		m = press(t, m, string(r))
	}
	return pressEnter(t, m)
}

func TestEscClosesAPickersFilterBeforeItClosesTheForm(t *testing.T) {
	m := pickerForm(t)
	for _, k := range []string{"/", "2"} {
		m = press(t, m, k)
	}
	if body := plain(m.formView()); strings.Contains(body, "7d") || !strings.Contains(body, "12h") {
		t.Fatalf("the filter did not narrow the picker to 12h and 24h:\n%s", body)
	}
	if bar := plain(m.footerFor(modeForm)); !strings.Contains(bar, "esc clear") {
		t.Errorf("esc is the filter's while one is held, and the footer still says otherwise:\n%s", bar)
	}

	m = press(t, m, "esc")

	if m.mode != modeForm || m.form == nil {
		t.Fatalf("esc with a filter open left the form (mode %v)", m.mode)
	}
	if body := plain(m.formView()); !strings.Contains(body, "7d") || !strings.Contains(body, "1h") {
		t.Errorf("esc did not clear the filter, the picker is still narrowed:\n%s", body)
	}
	if got := *m.form.bindings["ttl"]; got != "1h" {
		t.Errorf("clearing the filter moved the picker to %q, want the 1h it opened on", got)
	}
	if bar := plain(m.footerFor(modeForm)); !strings.Contains(bar, "esc cancel") {
		t.Errorf("with no filter held esc cancels, and the footer does not say so:\n%s", bar)
	}

	// Something is typed in the first box, so leaving asks (formdiscard_test.go).
	if m = press(t, m, "esc"); m.dialog == nil || m.mode != modeForm {
		t.Fatal("esc on a picker with no filter did not ask about the form")
	}
	if m = press(t, m, "y"); m.mode == modeForm {
		t.Error("discarding did not close the form")
	}
}

func TestEscClearsAMultiSelectsFilterToo(t *testing.T) {
	m := pressEnter(t, pickerForm(t))
	for _, k := range []string{"/", "i"} {
		m = press(t, m, k)
	}
	if body := plain(m.formView()); strings.Contains(body, "table") {
		t.Fatalf("the filter did not narrow the list:\n%s", body)
	}

	m = press(t, m, "esc")

	if m.mode != modeForm {
		t.Fatalf("esc with a filter open left the form (mode %v)", m.mode)
	}
	if body := plain(m.formView()); !strings.Contains(body, "table") {
		t.Errorf("esc did not clear the filter:\n%s", body)
	}
	if got := m.form.bindings["name"]; got == nil || *got != "keep-me" {
		t.Errorf("what was typed in the first box is %v, want it kept", got)
	}
}

// A filter opened and closed with nothing typed holds no esc either: the key
// cancels as it does on any other box.
func TestEscAfterAnEmptyFilterIsClosedStillCancels(t *testing.T) {
	m := pickerForm(t)
	m = press(t, m, "/")
	m = press(t, m, "esc")
	if m.mode != modeForm {
		t.Fatalf("esc on an open, empty filter left the form (mode %v)", m.mode)
	}
	// Something is typed in the first box, so leaving asks (formdiscard_test.go).
	if m = press(t, m, "esc"); m.dialog == nil || m.mode != modeForm {
		t.Fatal("esc with the filter closed did not ask about the form")
	}
	if m = press(t, m, "y"); m.mode == modeForm {
		t.Error("discarding did not close the form")
	}
}
