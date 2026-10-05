package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func quickPlugin(t *testing.T, caps ...plugin.Capability) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func ranText(body string) plugin.Handler {
	return func(context.Context, plugin.Request) (view.View, error) {
		return view.Text{Body: body}, nil
	}
}

// sys.cpu took two enters, time.at three and gen.password eight to show what
// the run would have produced from the form's untouched boxes. A read with
// nothing required runs on enter; anything that has to be asked, changed or
// confirmed keeps its screen exactly as before.
func TestEnterRunsAReadThatNeedsNothingAndOpensAFormForTheRest(t *testing.T) {
	optional := []plugin.Field{
		{Name: "length", Type: plugin.Int, Default: 20},
		{Name: "symbols", Type: plugin.Bool},
	}
	cases := []struct {
		name string
		c    plugin.Capability
		want mode
	}{
		{"a read with only optional inputs",
			plugin.Capability{ID: "demo.opt", Summary: "s", Safety: plugin.Read, Inputs: optional, Run: ranText("x")},
			modeRunning},
		{"a read with no inputs at all",
			plugin.Capability{ID: "demo.none", Summary: "s", Safety: plugin.Read, Run: ranText("x")},
			modeRunning},
		{"a read with a required input",
			plugin.Capability{ID: "demo.need", Summary: "s", Safety: plugin.Read, Run: ranText("x"),
				Inputs: []plugin.Field{{Name: "target", Type: plugin.String, Positional: true, Required: true}}},
			modeForm},
		{"a read whose input arrives on a pipe, which a form cannot supply",
			plugin.Capability{ID: "demo.piped", Summary: "s", Safety: plugin.Read, Run: ranText("x"),
				Inputs: []plugin.Field{{Name: "body", Type: plugin.String, Positional: true, Piped: true}}},
			modeForm},
		{"a read with a credential nothing has supplied",
			plugin.Capability{ID: "demo.unlock", Summary: "s", Safety: plugin.Read, Run: ranText("x"),
				Inputs: []plugin.Field{{Name: "passphrase", Type: plugin.Secret}}},
			modeForm},
		{"a write with only optional inputs",
			plugin.Capability{ID: "demo.write", Summary: "s", Safety: plugin.Write, Inputs: optional, Run: ranText("x")},
			modeForm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(quickPlugin(t, tc.c), config.Dashboard{}, nil)
			opened, cmd := m.open(tc.c)
			got := opened.(Model)
			if got.mode != tc.want {
				t.Fatalf("enter on %s left the shell in mode %v, want %v", tc.c.ID, got.mode, tc.want)
			}
			if tc.want == modeRunning && cmd == nil {
				t.Fatal("the run was announced and never started")
			}
		})
	}
}

// A destructive capability goes through its dry run whatever it asks, so the
// quick path must not become a way round the consent screen.
func TestEnterOnADestructiveCapabilityStillStopsAtTheDryRun(t *testing.T) {
	c := plugin.Capability{ID: "demo.boom", Summary: "s", Safety: plugin.Destructive,
		Inputs: []plugin.Field{{Name: "force", Type: plugin.Bool}},
		Run:    ranText("x")}
	m := New(quickPlugin(t, c), config.Dashboard{}, nil)
	opened, _ := m.open(c)
	if got := opened.(Model).mode; got != modeForm {
		t.Fatalf("a destructive capability with inputs opened %v, want its form", got)
	}
	bare := plugin.Capability{ID: "demo.bare", Summary: "s", Safety: plugin.Destructive, Run: ranText("x")}
	opened, _ = New(quickPlugin(t, bare), config.Dashboard{}, nil).open(bare)
	if got := opened.(Model).mode; got != modeRunning {
		t.Fatalf("a destructive capability with nothing to ask opened %v, want its dry run", got)
	}
	if !opened.(Model).previewing {
		t.Fatal("it ran for real instead of previewing")
	}
}

// A credential another layer has answered no longer needs the box.
func TestAnAnsweredCredentialDoesNotKeepTheForm(t *testing.T) {
	c := plugin.Capability{ID: "demo.unlock", Summary: "s", Safety: plugin.Read, Run: ranText("x"),
		Inputs: []plugin.Field{{Name: "passphrase", Type: plugin.Secret}}}
	m := New(quickPlugin(t, c), config.Dashboard{}, nil)
	if m.answered(c, c.Inputs[0], nil) {
		t.Fatal("an empty base answered the credential")
	}
	if !m.answered(c, c.Inputs[0], map[string]any{"passphrase": "from the store session"}) {
		t.Fatal("a credential the store session supplied was asked for again")
	}
}

// The search bar and a catalogue row both end at open, so one rule covers
// them: enter on a match or a row runs a read that needs nothing.
func TestTheSearchBarAndTheCatalogueRunAReadOnEnter(t *testing.T) {
	c := plugin.Capability{ID: "demo.opt", Summary: "needs nothing", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "length", Type: plugin.Int, Default: 20}}, Run: ranText("QUICK-RAN")}
	m := New(quickPlugin(t, c), config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	base := um.(Model)

	searching := press(t, base, "/")
	for _, r := range "demo.opt" {
		searching = press(t, searching, string(r))
	}
	if got := searching.searchResults(); len(got) != 1 || got[0].ID != "demo.opt" {
		t.Fatalf("the query found %v", got)
	}
	if got := press(t, searching, "enter"); got.mode != modeRunning {
		t.Fatalf("enter on a search match left the shell in mode %v, want the run", got.mode)
	}

	browsing := press(t, base, ":")
	if !browsing.onCapability() {
		t.Fatal("the catalogue opened with the cursor on a section label")
	}
	if got := press(t, browsing, "enter"); got.mode != modeRunning {
		t.Fatalf("enter on a catalogue row left the shell in mode %v, want the run", got.mode)
	}
}

// `e` on the result is the way back to the boxes, and it opens them on what
// the run used.
func TestEditInputsReopensTheFormAfterAQuickRun(t *testing.T) {
	c := plugin.Capability{ID: "demo.opt", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "length", Type: plugin.Int, Default: 20}}, Run: ranText("QUICK-RAN")}
	m := New(quickPlugin(t, c), config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	opened, _ := um.(Model).open(c)
	landed, _ := opened.(Model).Update(resultMsg{cap: c, view: view.Text{Body: "QUICK-RAN"}, seq: opened.(Model).runSeq})
	rm := landed.(Model)
	if rm.mode != modeResult {
		t.Fatalf("mode = %v, want the result", rm.mode)
	}
	edited := press(t, rm, "e")
	if edited.mode != modeForm {
		t.Fatalf("e left the shell in mode %v, want the form", edited.mode)
	}
}

// Every terminal can send ctrl+s, which is what makes it the submit key to
// name; shift+enter and alt+enter arrive only where the terminal was set up
// for them.
func TestCtrlSSubmitsAFormWhereverFastSubmitDoes(t *testing.T) {
	c := plugin.Capability{ID: "demo.write", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "a", Type: plugin.String, Default: "x"},
			{Name: "b", Type: plugin.Int, Default: 3},
		},
		Run: ranText("CTRL-S-RAN")}
	tm := newTestModel(t, New(quickPlugin(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: ':', Text: ":"})
	waitFor(t, tm, "demo.write")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "ctrl+s submit")
	tm.Send(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	waitFor(t, tm, "CTRL-S-RAN")
	quit(t, tm)
}
