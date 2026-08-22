package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// PROJECT.md D76: alt+enter is a second, independent way into fast-submit —
// what a real ESC+CR byte pair (VS Code's own Claude-Code-installed
// shift+enter terminal keybinding, or "Option as Meta" on another terminal)
// actually decodes to, proven against the real decoder bubbletea uses
// rather than assumed from reading its source.
func TestAltEnterIsWhatEscThenCarriageReturnDecodesTo(t *testing.T) {
	var dec uv.EventDecoder
	n, ev := dec.Decode([]byte{0x1b, '\r'})
	if n != 2 {
		t.Fatalf("consumed %d bytes, want 2 (ESC+CR as one key event)", n)
	}
	kp, ok := ev.(uv.KeyPressEvent)
	if !ok {
		t.Fatalf("event = %T, want a KeyPressEvent", ev)
	}
	got := tea.KeyPressMsg{Code: kp.Code, Mod: tea.KeyMod(kp.Mod)}.String()
	if got != "alt+enter" {
		t.Errorf("ESC+CR decoded to %q, want %q", got, "alt+enter")
	}
}

func TestAltEnterRunsACapabilityWithCurrentDefaults(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.quick", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "a", Type: plugin.String, Default: "x"},
			{Name: "b", Type: plugin.Int, Default: 3},
		},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "ALT-QUICK-SUBMIT-RAN"}, nil
		},
	}
	tm := teatest.NewTestModel(t, New(fastFormRegistry(t, c), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 'b', Text: "b"})
	waitFor(t, tm, "demo.quick")
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	waitFor(t, tm, "a")

	tm.Send(altEnter)
	waitFor(t, tm, "ALT-QUICK-SUBMIT-RAN")

	quit(t, tm)
}

func TestAltEnterOnAnEmptyThemeFormSavesNoOverrides(t *testing.T) {
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	tm := teatest.NewTestModel(t, New(registry.New(), config.Dashboard{}, nil), teatest.WithInitialTermSize(100, 40))
	tm.Send(tea.KeyPressMsg{Code: 't', Text: "t"})
	waitFor(t, tm, "primary")

	tm.Send(altEnter)
	waitFor(t, tm, "reset to the built-in theme")

	quit(t, tm)
}

func TestAltEnterOnTheCopyPickerAcceptsTheDefaultChoice(t *testing.T) {
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

	tm.Send(altEnter)
	waitFor(t, tm, "copied value")

	quit(t, tm)
}
