package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A plugin's declaration crosses the wire as JSON, where every number is a
// float64, and the flag was declared from `f.Default.(int)`, which is zero for
// one: a flag whose default is zero shows none in --help, so the port a
// database would be reached on and the limit a list is cut at were left out of
// the very page that says what a flag does.
func TestAPluginIntDefaultThatArrivedAsAFloatIsShownInHelp(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "wire", Summary: "a plugin whose declaration came over the wire",
		Capabilities: []plugin.Capability{{
			ID: "wire.list", Summary: "list", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "limit", Type: plugin.Int, Default: float64(200), Min: 1, Max: 1000, Help: "how many to return"}},
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, reg, "wire", "list", "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(200)") {
		t.Errorf("the default is not in --help:\n%s", out)
	}
}
