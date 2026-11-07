package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// searched types query into the dashboard's search bar and presses enter.
func searched(t *testing.T, query string) Model {
	t.Helper()
	m, _ := realModel(t, 120, 40)
	m = press(t, m, "/")
	m.query = query
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(Model)
}

// The hints on this screen write a call as `grant.allow target=sys.ps
// ttl=15m`, and typed into the bar that way it found grant.allow and opened its
// form empty. The words find the capability; what the call states fills its
// boxes, for the person to read and send.
func TestACallTypedIntoTheSearchBarFillsItsForm(t *testing.T) {
	m := searched(t, "grant.allow target=sys.ps ttl=15m")
	if m.form == nil || m.current.ID != "grant.allow" {
		t.Fatalf("enter opened %v on %q, want grant.allow's form", m.mode, m.current.ID)
	}
	for name, want := range map[string]string{"target": "sys.ps", "ttl": "15m"} {
		got := "(no such box)"
		if b, ok := m.form.bindings[name]; ok {
			got = *b
		}
		if got != want {
			t.Errorf("the %s box holds %q, want %q", name, got, want)
		}
	}
}

// A hint's placeholder is left for the form to ask, an input the capability
// does not have is refused with the bar left open on it, and a secret is never
// taken from the bar, which shows what is typed into it.
func TestACallTypedIntoTheSearchBarIsHeldToItsInputs(t *testing.T) {
	m := searched(t, "grant.allow target=<capability> ttl=1h")
	if m.form == nil || *m.form.bindings["target"] != "" || *m.form.bindings["ttl"] != "1h" {
		t.Errorf("a placeholder was typed into the box, or the rest was dropped")
	}
	for query, want := range map[string]string{
		"grant.allow nonsense=1":     "grant.allow has no input nonsense",
		"kv.set key=db value=s3cret": "value is a secret",
	} {
		m := searched(t, query)
		if m.form != nil || !m.searchEditing || !strings.Contains(m.flashBad, want) {
			t.Errorf("%q: form %v, bar open %v, refusal %q — want the bar kept with %q",
				query, m.form != nil, m.searchEditing, m.flashBad, want)
		}
	}
}
