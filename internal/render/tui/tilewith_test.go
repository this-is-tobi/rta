package tui

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A tile's `with:` naming an input its capability does not take used to be
// dropped on every refresh, and the tile ran without the value: after a
// plugin renamed `sslmode` to `tls`, a tile stating `sslmode: verify-full`
// read at the plugin's default mode. The tile draws the refusal instead, and
// its capability never runs.
func TestATileWithAnInputItsCapabilityDoesNotTakeDrawsTheRefusal(t *testing.T) {
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	runs := 0
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Renamed: map[string]string{"sslmode": "tls"},
		Capabilities: []plugin.Capability{{
			ID: "db.status", Summary: "status", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "tls", Type: plugin.String, Config: "tls"}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				runs++
				return view.Text{Body: "up"}, nil
			},
		}}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "db.status", With: map[string]any{"sslmode": "verify-full"}}}}, nil)
	m = filled(t, m, 80, 24)
	msg, ok := tileCmd(1, m.tiles[1], statedConfig{}, "", nil, config.Connection{})().(tileMsg)
	if !ok {
		t.Fatal("the tile's command answered with no tile message")
	}
	if msg.err == nil || msg.err.Code != "core.dashboard.input.unknown" || msg.err.Hint != `db takes "tls" in its place` {
		t.Fatalf("the tile drew %+v", msg.err)
	}
	if runs != 0 {
		t.Errorf("the capability ran %d times without the value", runs)
	}
}
