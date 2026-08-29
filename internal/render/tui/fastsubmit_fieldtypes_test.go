package tui

import (
	"bytes"
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// fastsubmit_test.go proves the mechanism against String/Int fields, a
// Destructive capability with no Inputs of its own, and a two-stage Prefill
// form built from plain Strings. Reported as "shift+enter is not available on
// every form" — traced by hand first: every mode that opens a huh.Form
// (modeForm, covering a capability run, a two-stage edit and the plugin
// config editor alike, plus modeTheme and modeCopyPick) shares exactly one
// "case \"shift+enter\":" with nothing upstream that could intercept the key
// first, confirmed against every m.mode = modeForm call site. That left field
// *shape* as the only place a real gap could still hide — huh gives each
// field type its own Update, and nothing here had ever driven a Bool, an
// Options-driven Select or MultiSelect, a Secret, a Path, or a multi-line
// Text field through the synthetic-Enter path at all. These close that gap;
// every one of them passed on the first correctly-written attempt, so the
// wiring was already complete — this is coverage, not a fix.

func TestShiftEnterAcceptsABoolFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.bool", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "flag", Type: plugin.Bool, Default: true}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "BOOL-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.bool")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "flag")

	tm.Send(shiftEnter)
	waitFor(t, tm, "BOOL-RAN")

	quit(t, tm)
}

func TestShiftEnterAcceptsAClosedSetSelectFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.select", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "kind", Type: plugin.String, Default: "b", Options: []string{"a", "b", "c"}}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "SELECT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.select")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "kind")

	tm.Send(shiftEnter)
	waitFor(t, tm, "SELECT-RAN")

	quit(t, tm)
}

func TestShiftEnterAcceptsAClosedSetMultiSelectFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.multiselect", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "tags", Type: plugin.StringSlice, Default: []string{"a"}, Options: []string{"a", "b", "c"}}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "MULTISELECT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.multiselect")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "tags")

	tm.Send(shiftEnter)
	waitFor(t, tm, "MULTISELECT-RAN")

	quit(t, tm)
}

func TestShiftEnterAcceptsAFreeformStringSliceFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.slice", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "tag", Type: plugin.StringSlice, Default: []string{"x", "y"}}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "SLICE-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.slice")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "tag")

	tm.Send(shiftEnter)
	waitFor(t, tm, "SLICE-RAN")

	quit(t, tm)
}

func TestShiftEnterAcceptsASecretFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.secret", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "passphrase", Type: plugin.Secret, Default: "hunter2"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "SECRET-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.secret")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "passphrase")

	tm.Send(shiftEnter)
	waitFor(t, tm, "SECRET-RAN")

	quit(t, tm)
}

func TestShiftEnterAcceptsAPathFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.path", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "out", Type: plugin.Path, Default: "/tmp/demo"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "PATH-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.path")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "out")

	tm.Send(shiftEnter)
	waitFor(t, tm, "PATH-RAN")

	quit(t, tm)
}

// The one real risk in the bunch: todo.add/note.add/note.edit's own body
// field is exactly this shape (huh.NewText, multi-line, ExternalEditor), and
// a plain Enter in a text area is a newline in most editors. huh's own
// TextKeyMap says otherwise — plain "enter" is bound to Next/Submit, and
// "alt+enter"/"ctrl+j" are reserved for an actual newline
// (charm.land/huh/v2's keymap.go) — but a keymap table is not the same claim
// as the real event loop honoring it.
func TestShiftEnterAcceptsAMultilineTextFieldsCurrentDefault(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.text", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "body", Type: plugin.Text, Default: "seeded body"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "TEXT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.text")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "body")

	tm.Send(shiftEnter)
	waitFor(t, tm, "TEXT-RAN")

	quit(t, tm)
}

// The existing destructive-decline test (TestShiftEnterDeclinesADestructiveCapabilityByDefault)
// uses a capability with no Inputs of its own — just the trailing confirm
// field, kv.rekey's actual shape has real fields ahead of it (Bool +
// StringSlice). One shift+enter races through every field including the
// confirm one — huh.NewConfirm treats a plain Enter as Submit regardless of
// whether Toggle/Accept/Reject ever touched it (field_confirm.go), so there
// is no intermediate stop to wait for; the whole form completes declined in
// a single press, the same as the no-Inputs case.
func TestShiftEnterDeclinesADestructiveCapabilityWithRealInputsAheadOfTheConfirm(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.destructive", Summary: "s", Safety: plugin.Destructive,
		Inputs: []plugin.Field{
			{Name: "generate", Type: plugin.Bool, Default: true},
			{Name: "recipient", Type: plugin.StringSlice, Default: []string{"r1"}},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "DESTRUCTIVE-EXECUTED"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.destructive")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "generate")

	tm.Send(shiftEnter)
	waitFor(t, tm, "capabilities") // declined, back to browse — no intermediate stop

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	tm.WaitFinished(t, teatest.WithFinalTimeout(framePatience))
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(tm.FinalOutput(t))
	if bytes.Contains(buf.Bytes(), []byte("DESTRUCTIVE-EXECUTED")) {
		t.Error("shift+enter ran a destructive capability with real inputs without an explicit confirmation")
	}
}

// Every existing shift+enter test uses Read or Destructive safety. Write
// appends no trailing confirm field (form.go only does that for
// Destructive), so this safety level had never actually been driven through
// the fast-submit path at all.
func TestShiftEnterRunsAPlainWriteCapabilityDirectly(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.write", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "a", Type: plugin.String, Default: "x"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "WRITE-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.write")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "a")

	tm.Send(shiftEnter)
	waitFor(t, tm, "WRITE-RAN")

	quit(t, tm)
}
