package tui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

var shiftEnter = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}

// altEnter is the second, independent way into the same fast-submit path
// (PROJECT.md D76) — what a real ESC+CR byte pair decodes to (the "meta
// sends escape" convention, e.g. VS Code's own Claude-Code-installed
// shift+enter keybinding, or "Option as Meta" on another terminal), proven
// against the actual decoder in TestAltEnterIsWhatEscThenCarriageReturnDecodesTo.
var altEnter = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}

// shift+enter: an action key to accept whatever is currently bound across
// a form's remaining fields, without tabbing through each one.
//
// advanceFormBySyntheticEnter/settleForm/resolveCmd (form.go) get direct,
// bare-form unit coverage below — no teatest needed, since settleForm
// drains a field's own multi-hop cascade (Enter -> nextFieldMsg ->
// nextGroupMsg, each hop only reachable by feeding the previous one's Cmd
// back through Update) synchronously and Init() turned out not to matter:
// routing to the focused field is by selector index, set at construction,
// not by a .focused flag Init() would otherwise be needed to set.
//
// The safety-relevant properties — a required field actually blocking
// the run, a destructive confirm actually staying declined, a two-stage
// form actually stopping at its boundary — are still verified end to end
// through the real bubbletea/teatest program loop further down. Not
// because the mechanism needs it to work, but because those are the
// properties that matter most if something in this file's understanding
// of the mechanism turns out to be wrong in a way only the real event
// loop would expose — which is exactly what happened twice while this
// was being built (PROJECT.md's own entry for this feature has both).

// advanceFormBySyntheticEnter/settleForm: direct, no Model, no teatest.

func TestAdvanceFormBySyntheticEnterCompletesAFormWhereEveryFieldIsAlreadyValid(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "a", Type: plugin.String, Default: "x"},
			{Name: "b", Type: plugin.Int, Default: 3},
		},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)

	f := advanceFormBySyntheticEnter(cf.form)

	if f.State != huh.StateCompleted {
		t.Fatalf("State = %v, want StateCompleted — every field had a valid default", f.State)
	}
}

// The property the feature exists to preserve: a required field left
// blank refuses to advance, the same as a real Enter on it would, rather
// than "quickly submit" silently accepting a request with a required
// field missing.
func TestAdvanceFormBySyntheticEnterStopsAtARequiredFieldLeftBlank(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "first", Type: plugin.String, Default: "ok"},
			{Name: "target", Type: plugin.String, Required: true},
			{Name: "last", Type: plugin.String, Default: "ok"},
		},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)

	f := advanceFormBySyntheticEnter(cf.form)

	if f.State != huh.StateNormal {
		t.Fatalf("State = %v, want StateNormal — the required field was never filled in", f.State)
	}
	if len(f.Errors()) == 0 {
		t.Error("no error reported for the blank required field")
	}
}

// Same property, the other validation path: an Int field holding text
// that does not parse refuses to advance rather than being silently
// coerced to 0 by capForm.values()'s own discarded strconv error.
func TestAdvanceFormBySyntheticEnterStopsAtAnIntFieldHoldingText(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "count", Type: plugin.Int}}, // no default: starts empty
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	cf.form.Init() // focuses the first field — bubbles' textinput ignores keystrokes otherwise
	// Type through the field's own Update, not by mutating cf.bindings
	// directly: huh copies the bound pointer's value into its own
	// textinput.Model once, at construction (Input.Accessor), and
	// validates *that* copy — a pointer mutated afterward from outside is
	// never read again, so this is the only way to make huh's own
	// validation actually see "abc" the way a real keystroke would.
	f := cf.form
	for _, r := range "abc" {
		model, _ := f.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		if nf, ok := model.(*huh.Form); ok {
			f = nf
		}
	}

	f = advanceFormBySyntheticEnter(f)

	if f.State != huh.StateNormal {
		t.Fatalf("State = %v, want StateNormal — \"abc\" is not a valid int", f.State)
	}
	if len(f.Errors()) == 0 {
		t.Error("no error reported for the unparsable int field")
	}
}

// A form with several fields advances past more than one on a single
// settleForm call when each hop's own cascade resolves clean — proof the
// multi-hop draining (Enter -> nextFieldMsg -> ... ) actually moves the
// selector, not just that the outer loop in advanceFormBySyntheticEnter
// eventually gets there over several calls.
// One settleForm call fully drains the cascade one Enter starts — for a
// field that is not the group's last, that cascade is "submit a, move the
// selector to b, focus b" (Group.nextField, called synchronously once
// nextFieldMsg is fed back), never touching b itself. Completing the form
// still needs its own, second Enter for b — proven here by the negative:
// State stays Normal and the focused field actually changed, rather than
// silently remaining stuck on a with an unreported error.
func TestSettleFormAdvancesPastOneFieldOnASingleCall(t *testing.T) {
	c := plugin.Capability{
		ID: "x.y", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "a", Type: plugin.String, Default: "x"},
			{Name: "b", Type: plugin.String, Default: "y"},
		},
	}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	before := cf.form.GetFocusedField()

	f := settleForm(cf.form, tea.KeyPressMsg{Code: tea.KeyEnter})

	if f.State != huh.StateNormal {
		t.Fatalf("State = %v, want StateNormal — field b has not had its own Enter yet", f.State)
	}
	if len(f.Errors()) != 0 {
		t.Errorf("errors = %v, want none — field a's default is valid", f.Errors())
	}
	if f.GetFocusedField() == before {
		t.Error("the focused field did not change — settleForm did not advance past field a")
	}
}

// resolveCmd: the timing contract advanceFormBySyntheticEnter's whole
// approach rests on.

func TestResolveCmdReturnsAQuicklyResolvingMessage(t *testing.T) {
	msg, ok := resolveCmd(func() tea.Msg { return tea.WindowSizeMsg{Width: 1} })
	if !ok {
		t.Fatal("resolveCmd reported timeout for a Cmd that returns immediately")
	}
	if _, is := msg.(tea.WindowSizeMsg); !is {
		t.Errorf("resolveCmd returned %T, want the Cmd's own message unchanged", msg)
	}
}

// The property that makes the whole approach safe against a real cursor
// blink Cmd: a Cmd slower than the budget is reported as timed out, not
// waited on — and the goroutine it started must not block this call
// forever once its own timer eventually fires.
func TestResolveCmdTimesOutOnASlowCommand(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	_, ok := resolveCmd(func() tea.Msg {
		<-release
		return tea.WindowSizeMsg{}
	})

	if ok {
		t.Error("resolveCmd waited out a Cmd slower than its budget instead of timing out")
	}
}

func fastFormRegistry(t *testing.T, c plugin.Capability) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// The happy path the feature exists for: every field already has a
// reasonable default, and shift+enter runs the capability with them
// rather than requiring a trip through each field in turn.
func TestShiftEnterRunsACapabilityWithCurrentDefaults(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.quick", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "a", Type: plugin.String, Default: "x"},
			{Name: "b", Type: plugin.Int, Default: 3},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "QUICK-SUBMIT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.quick")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "a") // form open, first field visible

	tm.Send(shiftEnter)
	waitFor(t, tm, "QUICK-SUBMIT-RAN")

	quit(t, tm)
}

// A required field left blank must block the run exactly the way it would
// through the normal per-field path — shift+enter is a shortcut through
// the same validation, not around it.
func TestShiftEnterDoesNotRunWithARequiredFieldBlank(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.needstarget", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "target", Type: plugin.String, Required: true}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "SHOULD-NOT-RUN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.needstarget")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "target")

	tm.Send(shiftEnter)
	waitFor(t, tm, "target is required") // validatorFor's own message, still on screen

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(tm.FinalOutput(t))
	if bytes.Contains(buf.Bytes(), []byte("SHOULD-NOT-RUN")) {
		t.Error("shift+enter ran a capability whose required field was left blank")
	}
}

// The property that matters most: fast-submitting a destructive
// capability's form must not confirm it. huh.NewConfirm's own Next/Submit
// handling (field_confirm.go) never touches the bound value on plain
// Enter — only explicit Toggle/Accept/Reject do — so a Confirm nobody
// answered stays at its bound zero value, which capForm seeds false
// (declined) for exactly this field (form.go's `ok := false`).
func TestShiftEnterDeclinesADestructiveCapabilityByDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.boom", Summary: "s", Safety: plugin.Destructive,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "BOOM-EXECUTED"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.boom")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "destructive — run it?")

	tm.Send(shiftEnter)
	waitFor(t, tm, "capabilities") // declined by default, back to browse

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(tm.FinalOutput(t))
	if bytes.Contains(buf.Bytes(), []byte("BOOM-EXECUTED")) {
		t.Error("shift+enter ran a destructive capability without an explicit confirmation")
	}
}

// A two-stage prefill form (identity fields, then the edit stage seeded
// from them) stops at the boundary rather than driving straight through:
// the second stage's fields are not built until the first stage
// completes, so there is nothing yet for a synthetic Enter to accept
// there — the operator sees the second stage open and presses shift+enter
// again if that one is fine as seeded too.
func TestShiftEnterStopsAtATwoStagePrefillBoundary(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.edit", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "id", Type: plugin.String, Positional: true, Required: true, Local: true},
			{Name: "note", Type: plugin.String, Default: "seeded"},
		},
		Prefill: func(context.Context, plugin.Request) (map[string]any, error) {
			return map[string]any{"note": "prefilled-from-record"}, nil
		},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "EDIT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.edit")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "id") // stage one: the identity field

	for _, r := range "42" {
		tm.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	tm.Send(shiftEnter)
	// Stage two opens, seeded from Prefill — not the run itself.
	waitFor(t, tm, "prefilled-from-record")

	quit(t, tm)
}

// fastSubmitThemeForm: every field is valid when left empty, so racing
// through an untouched theme form is itself a legitimate way to reach "no
// overrides".
func TestShiftEnterOnAnEmptyThemeFormSavesNoOverrides(t *testing.T) {
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	tm := teatest.NewTestModel(t, New(registry.New(), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 't', Text: "t"})
	waitFor(t, tm, "primary")

	tm.Send(shiftEnter)
	waitFor(t, tm, "reset to the built-in theme")

	quit(t, tm)
}

// fastSubmitCopyPick: functionally identical to plain enter for a
// single-field picker, wired in anyway so the shortcut means the same
// thing on every form-shaped screen rather than a set someone has to
// learn.
func TestShiftEnterOnTheCopyPickerAcceptsTheDefaultChoice(t *testing.T) {
	c := plugin.Capability{
		ID: "gen.password", Summary: "s", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Table{
				Columns: []view.Column{{Name: "Password"}},
				Rows:    [][]string{{"first-pw"}, {"second-pw"}},
			}, nil
		},
	}
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "gen", Summary: "gen", Capabilities: []plugin.Capability{c}}); err != nil {
		t.Fatal(err)
	}
	tm := teatest.NewTestModel(t, New(reg, config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "gen.password")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "first-pw")
	tm.Send(tea.KeyPressMsg{Code: 'c', Text: "c"})
	waitFor(t, tm, "copy which value?")

	tm.Send(shiftEnter)
	waitFor(t, tm, "copied value")

	quit(t, tm)
}
