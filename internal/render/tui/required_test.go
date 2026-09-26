package tui

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A run and a tile that reach the handler without a required input are
// refused by the host before it, whatever the form did or did not collect.
// The list picker submitted with nothing picked, and nothing after the form
// checked presence, so the handler ran without an input the CLI and MCP
// refuse to leave out. The picker is fixed; this is what holds the next form
// that forgets.
func TestARunWithoutARequiredInputIsRefusedBeforeTheHandler(t *testing.T) {
	ran := false
	c := plugin.Capability{ID: "demo.pick", Summary: "picks", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "kinds", Type: plugin.StringSlice, Required: true,
			Options: []string{"table", "view"}, Help: "kinds"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ran = true
			return view.Text{Body: "ran"}, nil
		},
	}
	c.Run = plugin.GuardInputs(c)

	for _, values := range []map[string]any{nil, {"kinds": []string{}}} {
		rm := runCmd(context.Background(), 1, c, values, false, statedConfig{}, "", nil, config.Connection{}, false)().(resultMsg)
		if rm.err == nil || rm.err.Code != "core.input.missing" || rm.err.Message != "demo.pick needs kinds" {
			t.Errorf("run %v: %+v", values, rm.err)
		}
	}
	// Worded for a tile, which has no box to fill in: its entry's with: is
	// where the input goes.
	tm := tileCmd(0, tile{cap: c}, statedConfig{}, "", nil, config.Connection{})().(tileMsg)
	if tm.err == nil || tm.err.Code != "core.input.missing" ||
		tm.err.Message != "demo.pick needs kinds, and a tile has no form to ask with" ||
		tm.err.Hint != "`with: {kinds: <value>}` in its dashboard entry states it for every run" {
		t.Errorf("tile: %+v", tm.err)
	}
	if ran {
		t.Fatal("the handler ran without its required input")
	}

	rm := runCmd(context.Background(), 1, c, map[string]any{"kinds": []string{"view"}}, false,
		statedConfig{}, "", nil, config.Connection{}, false)().(resultMsg)
	if rm.err != nil || !ran {
		t.Errorf("a run with the input given: %+v", rm.err)
	}
}

// A Piped input is required in the TUI — there is no pipe behind a form — and
// the host holds it there as it holds a Required one. The CLI alone may leave
// it out, and reads the pipe then.
func TestARunWithoutAPipedInputIsRefusedInTheTUI(t *testing.T) {
	ran := false
	c := plugin.Capability{ID: "demo.decode", Summary: "decodes", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "token", Type: plugin.Secret, Positional: true, Piped: true, Help: "token"}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			ran = true
			return view.Text{Body: "ran"}, nil
		},
	}
	c.Run = plugin.GuardInputs(c)
	rm := runCmd(context.Background(), 1, c, nil, false, statedConfig{}, "", nil, config.Connection{}, false)().(resultMsg)
	if rm.err == nil || rm.err.Code != "core.input.missing" || ran {
		t.Errorf("a run leaving the token out: %+v (ran %v)", rm.err, ran)
	}
	// A tile is not told to write a credential into its entry, which `--set`
	// refuses: the same way out `rta dashboard add` gives.
	tm := tileCmd(0, tile{cap: c}, statedConfig{}, "", nil, config.Connection{})().(tileMsg)
	if tm.err == nil || tm.err.Code != "core.input.missing" || ran ||
		tm.err.Hint != "token is a credential, which a tile cannot be given — run `rta demo decode` when you have one" {
		t.Errorf("a tile leaving the token out: %+v (ran %v)", tm.err, ran)
	}
}
