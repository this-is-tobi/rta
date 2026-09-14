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

// One footer per form, in the app's own vocabulary. huh used to draw a second
// help line inside the frame — "tab complete • enter next" on a box, "↑ up •
// ↓ down • / filter • shift+tab back • enter select" on a picker — so a form
// carried two bars that disagreed on separators and on the words for the same
// key. huh's is off; the footer says what the focused field can do instead.
func TestTheFormFooterSpeaksForTheFocusedField(t *testing.T) {
	noHistory(t)
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	cases := map[string]struct {
		first plugin.Field
		want  []string
		never []string
	}{
		"a picker": {
			first: plugin.Field{Name: "kind", Type: plugin.String, Options: []string{"a", "b"}},
			want:  []string{"↑↓ pick", "/ filter", "enter next", "esc cancel"},
			never: []string{"tab complete"},
		},
		"a box that completes": {
			first: plugin.Field{Name: "tag", Type: plugin.String,
				Suggest: func(context.Context, plugin.Request) []string { return []string{"x"} }},
			want:  []string{"tab complete", "↑↓ browse", "enter next"},
			never: []string{"filter"},
		},
		"a plain box": {
			first: plugin.Field{Name: "name", Type: plugin.String},
			want:  []string{"enter next", "⇧enter submit", "esc cancel"},
			never: []string{"complete", "browse", "filter"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := plugin.Capability{ID: "demo.thing", Summary: "s", Safety: plugin.Read,
				Inputs: []plugin.Field{tc.first, {Name: "other", Type: plugin.String}}, Run: run}
			m := New(fastFormRegistry(t, c), config.Dashboard{}, nil)
			um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
			opened, _ := um.(Model).open(c)
			fm := opened.(Model)
			if fm.mode != modeForm {
				t.Fatalf("mode = %v, want a form", fm.mode)
			}
			footer := plain(fm.footerFor(modeForm))
			for _, w := range tc.want {
				if !strings.Contains(footer, w) {
					t.Errorf("footer lacks %q:\n  %s", w, footer)
				}
			}
			for _, n := range tc.never {
				if strings.Contains(footer, n) {
					t.Errorf("footer promises %q for a field that cannot do it:\n  %s", n, footer)
				}
			}
			// And only one bar: huh's bullet-separated help must not be in
			// the frame above it.
			if body := plain(fm.formView()); strings.Contains(body, " • ") {
				t.Errorf("huh's own help line is still drawn inside the form:\n%s", body)
			}
		})
	}
}

// A validation message belongs in the footer too. With huh's help line off,
// huh reserved no row for one, so a required box left blank refused the run
// and its "target is required" grew the form by two lines into a panel that
// clips at a fixed height — the refusal happened and nothing on screen said
// why.
func TestAValidationErrorReachesTheFooter(t *testing.T) {
	noHistory(t)
	c := plugin.Capability{ID: "demo.needstarget", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "target", Type: plugin.String, Required: true}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil }}
	m := New(fastFormRegistry(t, c), config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	opened, _ := um.(Model).open(c)
	after, _ := opened.(Model).fastSubmitForm()
	am := after.(Model)
	if am.mode != modeForm {
		t.Fatalf("a blank required field let the form complete (mode %v)", am.mode)
	}
	if got := plain(am.footerFor(modeForm)); !strings.Contains(got, "target is required") {
		t.Errorf("the footer does not carry the validation message:\n  %s", got)
	}
	if body := plain(am.formView()); !strings.Contains(body, "target is required") {
		t.Errorf("the rendered form does not show why it refused:\n%s", body)
	}
}
