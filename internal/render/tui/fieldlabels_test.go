package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A web form marks a required box at its label, and says what a box takes in
// the words of the person filling it in. The form said `(string, required)` as
// the last words of the help, on the third line when the help wrapped, and
// named Go's types: stringSlice, text.

func labelsForm(t *testing.T) *capForm {
	t.Helper()
	c := plugin.Capability{
		ID: "demo.labels", Summary: "s", Safety: plugin.Write,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		Inputs: []plugin.Field{
			{Name: "url", Type: plugin.String, Required: true, Help: "request URL"},
			{Name: "note", Type: plugin.String, Help: "a word about it"},
			{Name: "header", Type: plugin.StringSlice, Help: "request header as 'Key: Value', repeatable"},
			{Name: "data", Type: plugin.Text, Help: "request body"},
			{Name: "ttl", Type: plugin.String, Required: true, Options: []string{"1h", "12h"}, Help: "how long it lasts"},
			{Name: "kinds", Type: plugin.StringSlice, Options: []string{"table", "view"}, Help: "what to list"},
			{Name: "count", Type: plugin.Int, Help: "how many"},
			{Name: "verbose", Type: plugin.Bool, Help: "say more"},
			{Name: "token", Type: plugin.SecretSlice, Help: "tokens to try"},
			{Name: "bare", Type: plugin.String},
		},
	}
	return tallForm(newCapForm(c, c.Inputs, nil, true, nil))
}

// tallForm is cf started and given the room to show every box at once.
func tallForm(cf *capForm) *capForm {
	cf.form = startedForm(cf).WithWidth(100).WithHeight(120)
	return cf
}

// boxRows is the rendered form with the focus gutter and the padding taken off
// each row, so a row can be compared to the words on it.
func boxRows(cf *capForm) []string {
	var out []string
	for _, l := range strings.Split(plain(cf.form.View()), "\n") {
		out = append(out, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "┃")))
	}
	return out
}

func hasLine(rows []string, want string) bool {
	for _, r := range rows {
		if r == want {
			return true
		}
	}
	return false
}

func TestRequiredIsOnTheTitleLineAndNotAfterTheHelp(t *testing.T) {
	cf := labelsForm(t)
	rows := boxRows(cf)
	for _, want := range []string{"url · required", "ttl · required"} {
		if !hasLine(rows, want) {
			t.Errorf("no title line reads %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	for _, optional := range []string{"note", "header", "data", "kinds", "count", "verbose", "token", "bare"} {
		if !hasLine(rows, optional) {
			t.Errorf("the optional %q has no plain title line:\n%s", optional, strings.Join(rows, "\n"))
		}
	}
	if joined := strings.Join(rows, "\n"); strings.Contains(joined, "required)") || strings.Contains(joined, ", required") {
		t.Errorf("required is still said after the help:\n%s", joined)
	}
}

func TestRequiredStaysSaidInWordsWithoutColour(t *testing.T) {
	cf := labelsForm(t)
	cf.noColor = true
	if !hasLine(boxRows(cf), "url · required") {
		t.Errorf("without colour the required box is not marked:\n%s", strings.Join(boxRows(cf), "\n"))
	}
}

func TestABoxIsDescribedInThePersonsWordsNotGos(t *testing.T) {
	rows := boxRows(labelsForm(t))
	for _, want := range []string{
		"request URL",
		"how long it lasts",
		"say more",
		"request header as 'Key: Value', repeatable (list, comma-separated)",
		"request body (multi-line) — ctrl+e opens $EDITOR",
		"what to list (space to toggle)",
		"how many (int)",
		"tokens to try (secret list, comma-separated)",
	} {
		if !hasLine(rows, want) {
			t.Errorf("no row reads %q:\n%s", want, strings.Join(rows, "\n"))
		}
	}
	joined := strings.Join(rows, "\n")
	for _, jargon := range []string{"(string", "stringSlice", "secretSlice", "(text", "(bool"} {
		if strings.Contains(joined, jargon) {
			t.Errorf("the form still says %q:\n%s", jargon, joined)
		}
	}
}

// A string with no help had nothing to say but its type, and now has nothing:
// no empty parentheses, no dangling dash before a completion hint.
func TestAStringWithNoHelpHasNoDescriptionRow(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.bare", Summary: "s", Safety: plugin.Write,
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		Inputs: []plugin.Field{{
			Name: "name", Type: plugin.String,
			Suggest: func(context.Context, plugin.Request) []string { return []string{"alpha"} },
		}},
	}
	rows := boxRows(tallForm(newCapForm(c, c.Inputs, nil, true, nil)))
	if !hasLine(rows, "tab completes, ↑↓ browse") {
		t.Errorf("a completing box with no help should say how it completes, and only that:\n%s", strings.Join(rows, "\n"))
	}
	for _, r := range rows {
		if strings.HasPrefix(r, "—") || strings.HasPrefix(r, "(") {
			t.Errorf("a row starts with a dangling %q:\n%s", r, strings.Join(rows, "\n"))
		}
	}
}
