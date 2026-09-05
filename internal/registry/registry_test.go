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
