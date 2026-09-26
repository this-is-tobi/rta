package tui

import (
	"testing"

	"charm.land/huh/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A required list picked from options is a box like any other required one:
// the form does not go past it with nothing picked. Every other required box
// is held by validatorFor; the picker had no validator at all, so an enter
// on it submitted the form without the list — and the host checks no input's
// presence on a run, so the handler ran without an input the CLI and MCP
// refuse to leave out.
func TestARequiredListIsNotSubmittedWithNothingPicked(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "kinds", Type: plugin.StringSlice, Required: true,
			Options: []string{"table", "view"}}}}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := advanceFormBySyntheticEnter(cf.form)
	if f.State != huh.StateNormal || len(f.Errors()) == 0 {
		t.Fatalf("State = %v, errors = %v: the list went through with nothing picked", f.State, f.Errors())
	}

	// And with something picked, it goes through.
	cf = newCapForm(c, c.Inputs, map[string]any{"kinds": []string{"view"}}, true, nil)
	if f := advanceFormBySyntheticEnter(cf.form); f.State != huh.StateCompleted {
		t.Fatalf("State = %v, errors = %v with view picked", f.State, f.Errors())
	}
}

// A closed set offers "(none)" only where an answer may be left out, which is
// the rule the box's own description states. A Piped input is required on
// every surface but the CLI, so its picker said "required" and offered
// "(none)" first, which submitted it empty. pkg/plugin refuses Options beside
// Piped, so no declaration reaches this; the form's rule does not lean on it.
func TestAPipedPickerOffersNoEmptyAnswer(t *testing.T) {
	c := plugin.Capability{ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "alg", Type: plugin.String, Piped: true,
			Options: []string{"hs256", "rs256"}}}}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := advanceFormBySyntheticEnter(cf.form)
	if f.State != huh.StateCompleted {
		t.Fatalf("State = %v, errors = %v", f.State, f.Errors())
	}
	if got := cf.values()["alg"]; got != "hs256" {
		t.Errorf("alg = %v, want the first option — a required picker has no empty answer", got)
	}
}

// A form box says what the host would refuse, as it is typed: a number out of
// its range is named in the footer rather than discovered after the run.
func TestAFormBoxHoldsANumberToItsRange(t *testing.T) {
	port := plugin.Field{Name: "port", Type: plugin.Int, Min: 1, Max: 65535}
	ratio := plugin.Field{Name: "ratio", Type: plugin.Float, Min: 0.0, Max: 1.0}
	limit := plugin.Field{Name: "limit", Type: plugin.Int, Min: 1}
	for _, tc := range []struct {
		f    plugin.Field
		in   string
		want string
	}{
		{port, "70000", "must be from 1 to 65535"},
		{port, "0", "must be from 1 to 65535"},
		{port, "443", ""},
		{port, "four", "must be an integer"},
		{ratio, "1.5", "must be from 0 to 1"},
		{ratio, "0.25", ""},
		{limit, "0", "must be at least 1"},
		{limit, "", ""}, // not given: the default fills it
	} {
		err := validatorFor(tc.f)(tc.in)
		got := ""
		if err != nil {
			got = err.Error()
		}
		if got != tc.want {
			t.Errorf("%s %q: %q, want %q", tc.f.Name, tc.in, got, tc.want)
		}
	}
}
