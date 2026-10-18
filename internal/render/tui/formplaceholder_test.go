package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A capability's declared examples were promised to the TUI as a filled-in form
// (pkg/plugin/examples.go) and never reached it: the box for a query opened
// empty, with no sign of what goes in it. The first example's value for a box is
// now what the box shows while it is empty, and shows as an example: it is not
// a value, and nothing about it may be mistaken for one.

func exampleCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.query", Summary: "run a query", Safety: plugin.Write,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		Examples: []plugin.Example{
			{Title: "count the orders", Inputs: map[string]any{
				"sql": "select count(*) from orders", "tags": []string{"a", "b c"}, "rows": 5, "mode": "fast",
				"body": "a short body"}},
			{Title: "the open ones", Inputs: map[string]any{"sql": "select * from orders where open", "limit": 100}},
		},
		Inputs: []plugin.Field{
			{Name: "sql", Type: plugin.String, Positional: true, Required: true, Help: "the statement"},
			{Name: "note", Type: plugin.String, Help: "a word about it"},
			{Name: "tags", Type: plugin.StringSlice, Help: "labels"},
			{Name: "rows", Type: plugin.Int, Help: "how many"},
			{Name: "limit", Type: plugin.Int, Help: "most to read"},
			{Name: "mode", Type: plugin.String, Options: []string{"fast", "safe"}, Help: "how"},
			{Name: "body", Type: plugin.Text, Help: "the text"},
		},
	}
}

func exampleForm(t *testing.T) *capForm {
	t.Helper()
	c := exampleCap()
	return tallForm(newCapForm(c, c.Inputs, nil, true, nil))
}

func TestTheFirstExamplesValueIsTheEmptyBoxsPlaceholder(t *testing.T) {
	rows := boxRows(exampleForm(t))
	for _, want := range []string{
		"> e.g. select count(*) from orders",
		"> e.g. a, b c",
		"> e.g. 5",
		// Only the second example gives it, and the first to give a box a value
		// is the one that counts.
		"> e.g. 100",
	} {
		if !hasLine(rows, want) {
			t.Errorf("no row reads %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	joined := strings.Join(rows, "\n")
	if strings.Contains(joined, "select * from orders") {
		t.Errorf("the second example stood in where the first had a value:\n%s", joined)
	}
	// A box no example speaks of stays bare, and so does a picker: it shows its
	// choices already.
	if !strings.Contains(joined, "e.g. a short body") {
		t.Errorf("a body box shows no example:\n%s", joined)
	}
	if strings.Count(joined, "e.g.") != 5 {
		t.Errorf("want a placeholder on the five boxes the examples fill, and none elsewhere:\n%s", joined)
	}
}

// What an example shows is not an answer. Enter through every box leaves the
// call without the values, and a required box left empty is still refused.
func TestAPlaceholderIsNotAValue(t *testing.T) {
	cf := exampleForm(t)
	f := advanceFormBySyntheticEnter(cf.form)
	if f.State == huh.StateCompleted {
		t.Fatal("enter through every box accepted a required box that showed only an example")
	}
	if err := f.GetFocusedField().Error(); err == nil || !strings.Contains(err.Error(), "sql is required") {
		t.Errorf("the required box says %v, want it refused as empty", err)
	}

	c := exampleCap()
	c.Inputs = c.Inputs[1:] // the same boxes, none of them required
	optional := tallForm(newCapForm(c, c.Inputs, nil, true, nil))
	optional.form = advanceFormBySyntheticEnter(optional.form)
	if optional.form.State != huh.StateCompleted {
		t.Fatalf("enter through boxes that show examples did not complete (state %v)", optional.form.State)
	}
	if got := optional.values(); len(got) != 0 {
		t.Errorf("the untouched boxes handed back %v, want nothing", got)
	}
}

// Typing replaces it, and what is typed is the box's value.
func TestTypingOverAPlaceholderLeavesOnlyWhatWasTyped(t *testing.T) {
	cf := exampleForm(t)
	cf.form = typeInto(cf.form, "select 1")
	rows := boxRows(cf)
	if !hasLine(rows, "> select 1") {
		t.Errorf("the typed text is not the box's:\n%s", strings.Join(rows, "\n"))
	}
	if strings.Contains(strings.Join(rows, "\n"), "select count(*) from orders") {
		t.Errorf("the placeholder is still there beside the typed text:\n%s", strings.Join(rows, "\n"))
	}
	if got := cf.values()["sql"]; got != "select 1" {
		t.Errorf("sql = %v, want what was typed", got)
	}
}

var sgrBefore = regexp.MustCompile(`((?:\x1b\[[0-9;:]*m)+)[^\x1b]*$`)

// styleOf is the colour and attribute codes in force just before text in an
// ANSI-bearing view, which is what tells how it is drawn.
func styleOf(t *testing.T, raw, text string) string {
	t.Helper()
	i := strings.Index(raw, text)
	if i < 0 {
		t.Fatalf("%q is not in the view:\n%q", text, raw)
	}
	m := sgrBefore.FindStringSubmatch(raw[:i])
	if m == nil {
		return ""
	}
	return m[1]
}

// Drawn muted, in a style of its own: a different one from a typed value, so
// the eye tells an example from an answer without reading either.
func TestAPlaceholderIsDrawnApartFromTypedText(t *testing.T) {
	empty := exampleForm(t)
	// From its second character: the text input draws the first under the cursor.
	placeholder := styleOf(t, empty.form.View(), ".g. select count(*) from orders")
	if placeholder == "" {
		t.Fatal("the placeholder carries no style at all")
	}

	typed := exampleForm(t)
	typed.form = typeInto(typed.form, "select 1")
	if got := styleOf(t, typed.form.View(), "select 1"); got == placeholder {
		t.Errorf("typed text and the placeholder are drawn alike (%q)", got)
	}
}

// With no colour a style says nothing, so the words have to: the box reads as
// an example in the text itself, and a copy of the screen keeps it.
func TestAPlaceholderStillReadsAsAnExampleWithNoColour(t *testing.T) {
	cf := exampleForm(t)
	cf.noColor = true
	rows := boxRows(cf)
	if !hasLine(rows, "> e.g. select count(*) from orders") {
		t.Errorf("without colour the example is not marked as one:\n%s", strings.Join(rows, "\n"))
	}
	if got := cf.values(); len(got) != 0 {
		t.Errorf("an untouched form handed back %v", got)
	}
}

// Only a keystroke the box takes writes to it.
func TestAPlaceholderIsNeverWrittenByTheEnterKey(t *testing.T) {
	cf := exampleForm(t)
	cf.form = settleForm(cf.form, tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := *cf.bindings["sql"]; got != "" {
		t.Errorf("enter wrote %q into the box", got)
	}
}
