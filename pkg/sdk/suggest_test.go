package sdk

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/wire"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// A plugin's Suggest is told which surface is asking.
//
// It was not, and the consequence was precise: a SuggestRequest carried no
// surface, so pkg/sdk built the request with none, which decodes to
// SurfaceUnknown — documented by pkg/plugin as "a direct in-process caller
// (tests, embedding code), which is inside the trust boundary". Built-ins were
// handed SurfaceCompletion by both surfaces that ask; every external plugin
// could not tell a keystroke from a unit test. So the rule pkg/plugin offers as
// the way to be safe on this path — "anything that would prompt, confirm, or
// take a visible moment must not run here" — was unavailable to exactly the
// plugins that reach off the machine.
func TestAnExternalPluginsSuggestIsToldItIsAKeystroke(t *testing.T) {
	var saw plugin.Surface
	p := plugin.Plugin{
		Name: "demo", Summary: "d",
		Capabilities: []plugin.Capability{{
			ID: "demo.run", Summary: "r", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "name", Type: plugin.String,
				Suggest: func(_ context.Context, req plugin.Request) []string {
					saw = req.Surface()
					return []string{"one"}
				}}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		}},
	}
	s := newServer(p)

	for _, want := range []plugin.Surface{plugin.SurfaceCompletion, plugin.SurfaceTUI, plugin.SurfaceCLI} {
		saw = ""
		resp, err := s.Suggest(context.Background(), &rtav1.SuggestRequest{
			CapabilityId: "demo.run", Field: "name",
			Surface: wire.SurfaceToProto(want),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.GetValues()) != 1 {
			t.Fatalf("no suggestions came back for %s", want)
		}
		if saw != want {
			t.Errorf("Suggest saw surface %q, want %q", saw, want)
		}
	}

	// An unset surface still means an in-process caller, which is what an
	// older host on the other end of the wire is.
	saw = ""
	if _, err := s.Suggest(context.Background(), &rtav1.SuggestRequest{
		CapabilityId: "demo.run", Field: "name",
	}); err != nil {
		t.Fatal(err)
	}
	if saw != plugin.SurfaceUnknown {
		t.Errorf("an unset surface decoded to %q, want the in-process zero", saw)
	}
}
