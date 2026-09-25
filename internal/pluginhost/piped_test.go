package pluginhost

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A plugin runs in its own process and never sees the CLI's standard input,
// so an input it marks Piped would be required on every surface but the CLI
// and read from nowhere on the CLI. The host refuses the declaration when it
// loads it, a cached one included, rather than accept a promise the plugin
// cannot keep.
func TestAPluginDeclaringAPipedInputIsRefused(t *testing.T) {
	declare := func(piped bool) plugin.Plugin {
		return plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{{
			ID: "demo.decode", Summary: "decode", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return nil, nil },
			Inputs: []plugin.Field{
				{Name: "token", Type: plugin.Secret, Positional: true, Piped: piped, Help: "the token"},
			},
		}}}
	}
	c := &Client{Identity: Identity{Path: "/plugins/rta-plugin-demo"}}
	err := c.adopt(wire.PluginToProto(declare(true)))
	if err == nil || !strings.Contains(err.Error(), "Piped") || !strings.Contains(err.Error(), "demo.decode") {
		t.Fatalf("a plugin's Piped input: %v, want a refusal naming it", err)
	}
	c = &Client{Identity: Identity{Path: "/plugins/rta-plugin-demo"}}
	if err := c.adopt(wire.PluginToProto(declare(false))); err != nil {
		t.Fatalf("the same declaration without it was refused: %v", err)
	}
}
