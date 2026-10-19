package sdk

import (
	"context"
	"slices"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
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

// The Suggest of a Live list is handed the element being typed, as the string
// the host sends under the field's own name: it reads it as a string, or as the
// list of that one element, and an empty one as no element at all.
func TestAnExternalPluginsLiveListSuggestIsHandedTheElementBeingTyped(t *testing.T) {
	var asString string
	var asList []string
	p := plugin.Plugin{
		Name: "demo", Summary: "d",
		Capabilities: []plugin.Capability{{
			ID: "demo.run", Summary: "r", Safety: plugin.Read,
			Inputs: []plugin.Field{{Name: "bucket", Type: plugin.StringSlice, Live: true,
				Suggest: func(_ context.Context, req plugin.Request) []string {
					asString, asList = req.String("bucket"), req.StringSlice("bucket")
					return []string{"media"}
				}}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		}},
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("a Live list was refused: %v", err)
	}
	s := newServer(p)

	for typed, want := range map[string][]string{"me": {"me"}, "": nil} {
		asString, asList = "?", nil
		if _, err := s.Suggest(context.Background(), &rtav1.SuggestRequest{
			CapabilityId: "demo.run", Field: "bucket",
			Values:  wire.ValuesToProto(map[string]any{"bucket": typed}),
			Surface: wire.SurfaceToProto(plugin.SurfaceCompletion),
		}); err != nil {
			t.Fatal(err)
		}
		if asString != typed {
			t.Errorf("req.String = %q for %q typed", asString, typed)
		}
		if !slices.Equal(asList, want) {
			t.Errorf("req.StringSlice = %q for %q typed, want %q", asList, typed, want)
		}
	}
}
