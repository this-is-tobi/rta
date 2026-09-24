package registry

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func testPlugin(name string) plugin.Plugin {
	return plugin.Plugin{
		Name:    name,
		Summary: "test",
		Capabilities: []plugin.Capability{{
			ID:      name + ".thing.list",
			Summary: "list things",
			Safety:  plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ok"}, nil
			},
		}},
	}
}

func TestRegisterAndLookup(t *testing.T) {
	r := New()
	if err := r.Register(testPlugin("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testPlugin("beta")); err != nil {
		t.Fatal(err)
	}

	if _, ok := r.Capability("alpha.thing.list"); !ok {
		t.Error("capability not found")
	}
	if _, ok := r.Capability("nope.thing.list"); ok {
		t.Error("phantom capability found")
	}
	if got := len(r.Plugins()); got != 2 {
		t.Errorf("Plugins() = %d, want 2", got)
	}
	caps := r.Capabilities()
	if len(caps) != 2 || caps[0].ID != "alpha.thing.list" {
		t.Errorf("Capabilities() not sorted: %v", caps)
	}
}

func TestRegisterRejectsDuplicateNamespace(t *testing.T) {
	r := New()
	if err := r.Register(testPlugin("dup")); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(testPlugin("dup")); err == nil {
		t.Error("duplicate namespace accepted")
	}
}

func TestRegisterRejectsInvalidPlugin(t *testing.T) {
	r := New()
	p := testPlugin("bad")
	p.Capabilities[0].Safety = "nope"
	if err := r.Register(p); err == nil {
		t.Error("invalid plugin accepted")
	}
}

// Every capability the registry hands out runs behind the check of its closed
// sets — on each surface's lookup, and in the plugin's own listing — and the
// declaration the caller registered is left as it was.
func TestARegisteredCapabilityHoldsItsOptions(t *testing.T) {
	p := testPlugin("gamma")
	p.Capabilities[0].Inputs = []plugin.Field{{Name: "mode", Type: plugin.String, Options: []string{"fast", "safe"}}}
	original := p.Capabilities[0].Run
	r := New()
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	c, _ := r.Capability("gamma.thing.list")
	listed := r.Plugins()[0].Capabilities[0]
	for _, got := range []plugin.Capability{c, listed} {
		_, err := got.Run(context.Background(), plugin.NewRequest(map[string]any{"mode": "quick"}, false, false))
		if verr := view.AsError(err, "test"); err == nil || verr.Code != "core.input.option" {
			t.Errorf("an unlisted option ran: %v", err)
		}
		if _, err := got.Run(context.Background(), plugin.NewRequest(map[string]any{"mode": "safe"}, false, false)); err != nil {
			t.Errorf("a listed option was refused: %v", err)
		}
	}
	if _, err := original(context.Background(), plugin.NewRequest(map[string]any{"mode": "quick"}, false, false)); err != nil {
		t.Errorf("the caller's own declaration was changed: %v", err)
	}
}
