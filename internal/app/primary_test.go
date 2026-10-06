package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func primaryRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "mint", Summary: "makes a value",
		Capabilities: []plugin.Capability{{
			ID: "mint.key.make", Summary: "make a key", Safety: plugin.Read,
			Primary: "key",
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.KeyValue{Pairs: []view.Pair{
					{Key: "key", Value: "k-1234"}, {Key: "strength", Value: "256 bits"},
				}}, nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// The tests' stdout is a buffer, which is what a pipe is to the renderer: the
// answer is the value alone, what else it says is on stderr, and the structured
// formats are what they were.
func TestACommandWithAPrimaryValueWritesItAloneToAPipe(t *testing.T) {
	reg := primaryRegistry(t)
	out, errOut, err := run(t, reg, "mint", "key", "make")
	if err != nil {
		t.Fatal(err)
	}
	if out != "k-1234\n" {
		t.Errorf("stdout is %q, want the value and a newline", out)
	}
	if !strings.Contains(errOut, "# strength: 256 bits") {
		t.Errorf("stderr is %q, want what else the answer says", errOut)
	}

	out, _, err = run(t, reg, "mint", "key", "make", "-o", "json")
	if err != nil || !strings.Contains(out, `"strength"`) || !strings.Contains(out, `"type": "keyvalue"`) {
		t.Errorf("-o json is %q (%v), want the whole view", out, err)
	}
}
