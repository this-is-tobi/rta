package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rule-them-all/internal/recent"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Completion inside a form, driven the way a person drives it.
//
// The unit tests beside these check what a field would offer; these check that
// the offer is reachable — which it was not, because rta documented tab and huh
// binds ctrl+e.

// typeInto presses each character of s into the form.
func typeInto(f *huh.Form, s string) *huh.Form {
	for _, r := range s {
		f = settleForm(f, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return f
}

// noHistory gives one test a data directory of its own, so what another test
// happened to run does not turn up in this one's completion list. The package
// as a whole already has one (TestMain); this narrows it to the test.
func noHistory(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

// startedForm inits the form the way a running program does, so the first
// field is focused and huh has computed its first round of suggestions.
func startedForm(cf *capForm) *huh.Form {
	f := cf.form
	if init := f.Init(); init != nil {
		if msg, ok := resolveCmd(init); ok {
			f = settleForm(f, msg)
		}
	}
	return settleForm(f, tea.WindowSizeMsg{Width: 80, Height: 24})
}

// A completing capability, with a second field whose answers depend on the
// first — the shape `grant allow` has.
func completingCap() plugin.Capability {
	return plugin.Capability{
		ID: "demo.allow", Summary: "allow", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "target", Type: plugin.String, Suggest: func(context.Context, plugin.Request) []string {
				return []string{"kv.get\tread one entry", "net.send"}
			}},
			{Name: "scope", Type: plugin.String, Suggest: func(_ context.Context, req plugin.Request) []string {
				return []string{req.String("target") + "-alpha", req.String("target") + "-beta"}
			}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
}

// Tab completes, which is what every field in this app says it does.
//
// huh binds AcceptSuggestion to ctrl+e and puts tab on "next field", so every
// suggestion rta computed was reachable only by a key nothing mentions —
// including the two fields whose own help text reads "press tab" and "tab
// completes paths".
func TestTabCompletesAField(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := typeInto(startedForm(cf), "kv.")
	f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := *cf.bindings["target"]; got != "kv.get" {
		t.Errorf("after tab, target = %q, want kv.get", got)
	}
}

// And the field below completes from the one above, as it is typed.
//
// One tab does both jobs: it takes the match and moves on, which is what tab
// did before this app had completion at all.
func TestTabCompletesFromWhatWasJustTyped(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := typeInto(startedForm(cf), "kv.")
	f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyTab})
	f = typeInto(f, "kv.get-b")
	f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyTab})
	if got := *cf.bindings["scope"]; got != "kv.get-beta" {
		t.Errorf("after tab, scope = %q, want it completed from the target above it", got)
	}
}

// Tab is never a dead key.
//
// bubbles only matches a suggestion against a non-empty value, so a box
// nobody has typed in has nothing to complete — which is the ordinary state of
// every field somebody is tabbing through. With tab bound only to completion
// it did nothing there, on every field of every form; on a masked box the text
// typed next went into the password, where nothing on screen would show it.
func TestTabAlwaysMovesOnWhenThereIsNothingToComplete(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.login", Summary: "login", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "password", Type: plugin.Secret, Local: true},
			{Name: "user", Type: plugin.String},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := typeInto(startedForm(cf), "hunter2")
	f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyTab})
	f = typeInto(f, "bob")

	if got := *cf.bindings["password"]; got != "hunter2" {
		t.Errorf("password = %q — the next field's text went into the masked box", got)
	}
	if got := *cf.bindings["user"]; got != "bob" {
		t.Errorf("user = %q — tab did not move the cursor off the credential", got)
	}
}

// Enter still moves on: tab took over completing, and nothing took over
// advancing.
func TestEnterStillMovesToTheNextField(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	f := typeInto(startedForm(cf), "net.send")
	f = settleForm(f, tea.KeyPressMsg{Code: tea.KeyEnter})
	f = typeInto(f, "x")
	if got := *cf.bindings["scope"]; got != "x" {
		t.Errorf("scope = %q — enter did not move the cursor onto it", got)
	}
}

// A field that completes says so, because the footer speaks for the screen and
// not for the box under the cursor.
func TestACompletingFieldSaysSo(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	out := startedForm(cf).View()
	if !strings.Contains(out, "tab completes") {
		t.Errorf("the field does not say it completes:\n%s", out)
	}
}

// A Secret and a Text field may not declare a completion at all: the list is
// drawn in plain text beside a box whose whole point is that its contents are
// not.
func TestSuggestIsRefusedOnASecretAndOnABody(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  plugin.FieldType
	}{{"secret", plugin.Secret}, {"body", plugin.Text}} {
		t.Run(tc.name, func(t *testing.T) {
			c := plugin.Capability{
				ID: "demo.x", Summary: "x", Safety: plugin.Read,
				Inputs: []plugin.Field{{Name: "v", Type: tc.typ, Local: tc.typ == plugin.Secret,
					Suggest: func(context.Context, plugin.Request) []string { return []string{"a"} }}},
				Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
			}
			err := plugin.Plugin{Name: "demo", Summary: "d", Capabilities: []plugin.Capability{c}}.Validate()
			if err == nil {
				t.Fatal("a completion list was accepted on a field that must not show one")
			}
			if !strings.Contains(err.Error(), "cannot declare Suggest") {
				t.Errorf("error = %v", err)
			}
		})
	}
}

// A form offers what this operator has used, behind whatever the field
// declared for itself.
//
// The inputs this matters for are the ones nothing can enumerate — a bucket, a
// database, a vault path — where there is no declared list at all and the
// shortlist is the whole of it. See internal/recent for why asking the service
// is not the build.
func TestAFormOffersWhatWasUsedBefore(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "store.list", Summary: "list", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String, Help: "which store"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	if got := newCapForm(c, c.Inputs, nil, true, nil).candidates(c.Inputs[0]); len(got) != 0 {
		t.Fatalf("candidates = %v before anything was used", got)
	}

	recent.Record(plugin.SurfaceCLI, c, map[string]any{"bucket": "mine"})
	got := newCapForm(c, c.Inputs, nil, true, nil).candidates(c.Inputs[0])
	if len(got) != 1 || got[0] != "mine" {
		t.Errorf("candidates = %v, want the bucket this operator used", got)
	}
}

// A declared list comes first and is not duplicated by the shortlist.
func TestWhatTheFieldDeclaresComesFirst(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{
		ID: "demo.run", Summary: "run", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "name", Type: plugin.String,
			Suggest: func(context.Context, plugin.Request) []string { return []string{"declared", "shared"} }}},
		Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	recent.Record(plugin.SurfaceCLI, c, map[string]any{"name": "shared"})
	recent.Record(plugin.SurfaceCLI, c, map[string]any{"name": "remembered"})

	got := newCapForm(c, c.Inputs, nil, true, nil).candidates(c.Inputs[0])
	if len(got) != 3 {
		t.Fatalf("candidates = %v, want the two declared plus the one remembered", got)
	}
	if got[0] != "declared" || got[1] != "shared" {
		t.Errorf("candidates = %v, want the declared list first", got)
	}
	if got[2] != "remembered" {
		t.Errorf("candidates = %v, want the remembered value last", got)
	}
}
