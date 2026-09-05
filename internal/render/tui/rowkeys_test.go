package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A lock is a kind and a name, and lock.rm needs both. The row shows both,
// under columns spelled like the inputs, so `x` on the list must run with
// the whole principal rather than open a form asking for the half already on
// screen — which is what feeding only the first column into the first key
// used to do.
func TestRowActionSeedsEveryKeyAColumnIsNamedFor(t *testing.T) {
	var got []string
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "lock", Summary: "locks",
		Capabilities: []plugin.Capability{
			{ID: "lock.list", Summary: "list", Safety: plugin.Read, Idempotent: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Table{
						Columns: []view.Column{{Name: "kind"}, {Name: "name"}, {Name: "note"}},
						Rows:    [][]string{{"agent", "claude", "loop"}, {"operator", "dash", "lost key"}},
						Total:   2,
					}, nil
				}},
			{ID: "lock.rm", Summary: "lift", Safety: plugin.Write, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "kind", Type: plugin.String, Positional: true, Required: true, Help: "k"},
					{Name: "name", Type: plugin.String, Positional: true, Required: true, Help: "n"},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					got = []string{req.String("kind"), req.String("name")}
					return view.Text{Body: "lifted"}, nil
				}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "lock.list"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	c, _ := reg.Capability("lock.list")
	v, err := c.Run(context.Background(), plugin.NewRequest(nil, false, false))
	if err != nil {
		t.Fatal(err)
	}
	withRes, _ := sized.(Model).Update(resultMsg{cap: c, view: v})
	rm := withRes.(Model)
	if !rm.interactive() {
		t.Fatal("lock.list result did not become an interactive list")
	}

	moved, _ := rm.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	acted, cmd := moved.(Model).Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	am := acted.(Model)
	if am.mode != modeRunning || am.current.ID != "lock.rm" {
		t.Fatalf("x: mode=%v current=%s, want lock.rm running without a form", am.mode, am.current.ID)
	}
	drain(t, cmd)
	if len(got) != 2 || got[0] != "operator" || got[1] != "dash" {
		t.Fatalf("lock.rm ran with %v, want [operator dash] (row 2's kind and name)", got)
	}
}

// The convention is additive: a list whose columns are not named for the
// keys still feeds its first column to the first key, as every list did
// before.
func TestRowActionFallsBackToTheFirstColumn(t *testing.T) {
	var doneLog []int
	m := listResult(t, listRegistry(t, &doneLog))
	moved, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	_, cmd := moved.(Model).Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	drain(t, cmd)
	if len(doneLog) != 1 || doneLog[0] != 2 {
		t.Fatalf("done ran with %v, want [2]", doneLog)
	}
}
