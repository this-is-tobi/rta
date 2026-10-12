package tui

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func bucketCap() plugin.Capability {
	return plugin.Capability{
		ID: "store.list", Summary: "list", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "bucket", Type: plugin.String, Config: "bucket"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
}

// A form is built for one environment, and what was used under another is not
// on its list.
func TestAFormOffersWhatWasUsedUnderItsProfileOnly(t *testing.T) {
	noHistory(t)
	c := bucketCap()
	recent.Record(plugin.SurfaceCLI, c, "staging", map[string]any{"bucket": "staging-assets"})
	recent.Record(plugin.SurfaceCLI, c, "prod", map[string]any{"bucket": "prod-assets"})
	recent.Record(plugin.SurfaceCLI, c, "", map[string]any{"bucket": "mine"})

	for profile, want := range map[string]string{"staging": "staging-assets", "prod": "prod-assets", "": "mine"} {
		cf := newCapForm(c, c.Inputs, nil, true, nil, withEnvironment(profile, nil))
		got := cf.candidates(c.Inputs[0])
		if len(got) != 1 || got[0] != want {
			t.Errorf("a form for %q offers %v, want only %q", profile, got, want)
		}
	}
}

// What a run through the TUI is given is remembered under the connection it
// went through, from what the form collected and not from the resolved request.
func TestARunIsRememberedUnderTheProfileItWentThrough(t *testing.T) {
	noHistory(t)
	c := bucketCap()
	msg := runCmd(context.Background(), 1, c, map[string]any{"bucket": "shop-assets"}, true,
		statedConfig{}, "staging", nil, config.Connection{}, false)()
	if res, ok := msg.(resultMsg); !ok || res.err != nil {
		t.Fatalf("the run did not succeed: %#v", msg)
	}
	used := recent.Load()
	if got := used.For("staging", c.ID, "bucket"); len(got) != 1 || got[0] != "shop-assets" {
		t.Errorf("under staging: %v", got)
	}
	if got := used.For("", c.ID, "bucket"); len(got) != 0 {
		t.Errorf("under the base configuration: %v", got)
	}
}
