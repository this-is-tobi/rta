package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// pressTab drives one tab the way a session does: into the model, which is
// where the key's meaning is decided, and then whatever the press asked the
// event loop to do next.
//
// These tests used to press it straight into the form, which was the same
// thing only while huh's own Next binding still held tab. It does not: huh
// tests Next before AcceptSuggestion and returns early when the value does
// not validate, so binding both made tab a dead key on a required empty box
// and a leave-the-field key on a completing one. Deciding here is the fix, and
// a form driven around the model no longer sees it.
func pressTab(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(tabKey)
	nm := next.(Model)
	if cmd == nil {
		return nm
	}
	msg := cmd()
	if msg == nil {
		return nm
	}
	next, _ = nm.Update(msg)
	return next.(Model)
}

// formModel wraps a bare capForm in the model that dispatches its keys, for
// the tests that build one directly.
func formModel(t *testing.T, cf *capForm) Model {
	t.Helper()
	m := New(registry.New(), config.Dashboard{}, nil)
	m.width, m.height = 100, 40
	m.mode, m.form = modeForm, cf
	m.form.form = startedForm(cf)
	return m
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
func startedForm(cf *capForm) *huh.Form { return startedHuhForm(cf.form) }

// startedHuhForm is the same for a form this package builds outside capForm —
// the theme editor, which answers tab by the same rule and so has to be
// driveable by the same test.
func startedHuhForm(f *huh.Form) *huh.Form {
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
	m := formModel(t, cf)
	m.form.form = typeInto(m.form.form, "kv.")
	m = pressTab(t, m)
	if got := *cf.bindings["target"]; got != "kv.get" {
		t.Errorf("after tab, target = %q, want kv.get", got)
	}
}

// And the field below completes from the one above, as it is typed.
//
// **Two presses, and the second one is the point.** The first takes the match
// and leaves the cursor where it is, so what was accepted is on screen and a
// path or a coordinate can take its next segment; the second finds nothing
// left to take and moves on. One press used to do both, which read as
// efficient and cost the whole shell rhythm: a completion you never see, and
// no way to complete twice in the same box.
func TestTabCompletesFromWhatWasJustTyped(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	m.form.form = typeInto(m.form.form, "kv.")

	focused := m.form.form.GetFocusedField()
	m = pressTab(t, m) // accepts "kv.get"
	if m.form.form.GetFocusedField() != focused {
		t.Fatal("the accept moved off the field — nothing shows what tab just took")
	}
	m = pressTab(t, m) // nothing extends it: on to the next field

	m.form.form = typeInto(m.form.form, "kv.get-b")
	m = pressTab(t, m)
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
//
// A masked box is the sharpest case for a second reason: it is never
// completed at all (a suggestion list renders in plain text beside a box that
// is masked on purpose), so it has no widget entry to fall back on and no
// ghost to accept. Tab there is only ever "move on", and it has to be.
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
	m := formModel(t, cf)
	m.form.form = typeInto(m.form.form, "hunter2")
	m = pressTab(t, m)
	m.form.form = typeInto(m.form.form, "bob")

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

// The arrows browse the offer from an empty box, which tab could only list.
//
// bubbles cycles suggestions on up and down already, but only among the
// matches of non-empty text, so on the box somebody arrives at first the
// arrows did nothing and the answer was "type the first letter". Down puts
// the first candidate in the box, the next down its neighbour, up walks the
// other way and wraps; typing over what was placed hands the arrows back to
// the widget.
func TestArrowsBrowseTheOfferFromAnEmptyBox(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)

	m = pressKey(t, m, downKey)
	if got := *cf.bindings["target"]; got != "kv.get" {
		t.Fatalf("after down on an empty box, target = %q, want kv.get", got)
	}
	if !strings.HasPrefix(m.flash, "1 of 2") {
		t.Errorf("flash = %q, want the position in the offer", m.flash)
	}
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["target"]; got != "net.send" {
		t.Fatalf("second down, target = %q, want net.send", got)
	}
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["target"]; got != "kv.get" {
		t.Fatalf("down past the end must wrap, target = %q", got)
	}
	m = pressKey(t, m, upKey)
	if got := *cf.bindings["target"]; got != "net.send" {
		t.Fatalf("up from the first must wrap to the last, target = %q", got)
	}
	if m.form.form.GetFocusedField() != huh.Field(cf.inputs["target"]) {
		t.Error("browsing left the box")
	}
}

func TestUpOnAnEmptyBoxStartsFromTheEnd(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	m = pressKey(t, m, upKey)
	if got := *cf.bindings["target"]; got != "net.send" {
		t.Errorf("up on an empty box, target = %q, want the last candidate", got)
	}
}

func TestTypingOverABrowsedCandidateHandsTheArrowsBack(t *testing.T) {
	noHistory(t)
	c := completingCap()
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	m := formModel(t, cf)
	m = pressKey(t, m, downKey)
	m.form.form = typeInto(m.form.form, "x")
	before := *cf.bindings["target"]
	m = pressKey(t, m, downKey)
	if got := *cf.bindings["target"]; got != before {
		t.Errorf("down on typed text replaced it: %q -> %q", before, got)
	}
}

func TestBrowseOnIsTheWholeRule(t *testing.T) {
	offered := []string{"a", "b", "c"}
	cases := []struct {
		typed string
		last  browsed
		down  bool
		at    int
		ok    bool
	}{
		{"", browsed{}, true, 0, true},
		{"", browsed{}, false, 2, true},
		{"a", browsed{"a", 0}, true, 1, true},
		{"c", browsed{"c", 2}, true, 0, true},
		{"a", browsed{"a", 0}, false, 2, true},
		{"ab", browsed{"a", 0}, true, 0, false},
		{"a", browsed{}, true, 0, false},
	}
	for _, tc := range cases {
		at, ok := browseOn(tc.typed, tc.last, offered, tc.down)
		if at != tc.at || ok != tc.ok {
			t.Errorf("browseOn(%q, %v, down=%v) = %d,%v want %d,%v", tc.typed, tc.last, tc.down, at, ok, tc.at, tc.ok)
		}
	}
	if _, ok := browseOn("", browsed{}, nil, true); ok {
		t.Error("nothing on offer must hand the key to the widget")
	}
}

var (
	downKey = tea.KeyPressMsg{Code: tea.KeyDown}
	upKey   = tea.KeyPressMsg{Code: tea.KeyUp}
)

// pressKey drives one key the way pressTab does: into the model, then
// whatever it asked the loop to do next.
func pressKey(t *testing.T, m Model, k tea.KeyPressMsg) Model {
	t.Helper()
	next, cmd := m.Update(k)
	nm := next.(Model)
	if cmd == nil {
		return nm
	}
	if msg := cmd(); msg != nil {
		next, _ = nm.Update(msg)
		nm = next.(Model)
	}
	return nm
}
