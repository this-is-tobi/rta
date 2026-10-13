package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// focusBox moves the cursor to the named box the way a person does, one field at
// a time, and fails if it never gets there.
func focusBox(t *testing.T, m Model, name string) Model {
	t.Helper()
	in, ok := m.form.inputs[name]
	if !ok {
		t.Fatalf("the form has no %q box", name)
	}
	for range 24 {
		if m.form.form.GetFocusedField() == huh.Field(in) {
			return m
		}
		m.form.form = settleForm(m.form.form, huh.NextField())
	}
	t.Fatalf("the cursor never reached the %q box", name)
	return m
}

// typeKeys presses each character into the model, as the event loop delivers
// them: one message at a time, whatever the key did next asked of it.
func typeKeys(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = pressKey(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
		if m.form == nil {
			t.Fatalf("the form closed after %q", string(r))
		}
	}
	return m
}

// **`profile` is the name of rta's own environment picker, and also the name of
// an input some capabilities declare for themselves** — grant.issue's "issue
// everything this environment covers", grant.allow's "only on this connection".
// A box of that name on a capability no profile can fill is a text box like any
// other; the form treated its first keystroke as the picker having moved,
// rebuilt itself on the "environment" just typed, and put the cursor back on the
// first field — so the rest of the word landed in `role`.
func TestTypingInAnInputNamedProfileKeepsTheCursorThere(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	reg := realRegistry(t)
	for _, id := range []string{"grant.issue", "grant.allow", "grant.renew", "grant.revoke"} {
		t.Run(id, func(t *testing.T) {
			c, ok := reg.Capability(id)
			if !ok {
				t.Fatalf("no %s", id)
			}
			if plugin.Profilable(c) {
				t.Fatalf("%s is one a profile can fill, so its `profile` is the picker", id)
			}
			m := New(reg, config.Dashboard{}, nil)
			m.width, m.height = 110, 62
			model, _ := m.startForm(c, nil)
			nm := model.(Model)
			nm.form.form = startedForm(nm.form)
			nm = focusBox(t, nm, "profile")
			before := map[string]string{}
			for name, bound := range nm.form.bindings {
				before[name] = *bound
			}

			nm = typeKeys(t, nm, "staging")

			if got := *nm.form.bindings["profile"]; got != "staging" {
				t.Errorf("the profile box holds %q after typing staging into it", got)
			}
			for name, bound := range nm.form.bindings {
				if name != "profile" && *bound != before[name] {
					t.Errorf("part of the word landed in %q: %q", name, *bound)
				}
			}
			if nm.form.form.GetFocusedField() != huh.Field(nm.form.inputs["profile"]) {
				t.Error("the cursor left the profile box while it was being typed in")
			}
		})
	}
}

// A role and an environment are one question with two boxes, and the refusal
// for answering both is the handler's (grant.issue.both), said when the form is
// submitted: nothing in the form complains about the first box while the second
// is being typed.
func TestARoleAndAProfileAreNotComplainedAboutWhileTheyAreTyped(t *testing.T) {
	noHistory(t)
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	reg := realRegistry(t)
	c, _ := reg.Capability("grant.issue")
	m := New(reg, config.Dashboard{}, nil)
	m.width, m.height = 110, 62
	model, _ := m.startForm(c, nil)
	nm := model.(Model)
	nm.form.form = startedForm(nm.form)

	nm = typeKeys(t, nm, "ops")
	nm = focusBox(t, nm, "profile")
	for _, r := range "staging" {
		nm = typeKeys(t, nm, string(r))
		if errs := nm.form.form.Errors(); len(errs) != 0 {
			t.Fatalf("the form complained while %q was being typed: %v", string(r), errs)
		}
	}
	values := nm.form.values()
	if values["role"] != "ops" || values["profile"] != "staging" {
		t.Errorf("values = %v, want the role and the profile as typed", values)
	}
}
