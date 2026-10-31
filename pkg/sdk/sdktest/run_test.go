package sdktest

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Run hands a handler what rta hands it: the declared defaults under the
// values given, the CLI as the surface; a call rta refuses is refused before
// the handler; and a nil *view.Error from the handler is its answer, as the
// host reads it.
func TestRunCallsAHandlerAsRtaDoes(t *testing.T) {
	var saw []plugin.Request
	c := plugin.Capability{ID: "demo.item.show", Summary: "show an item", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "name", Type: plugin.String, Required: true},
			{Name: "limit", Type: plugin.Int, Default: 10, Min: 1, Max: 100},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			saw = append(saw, req)
			var answered *view.Error
			return view.Text{Body: "ok"}, answered
		}}
	p := plugin.Plugin{Name: "demo", Summary: "demo", Capabilities: []plugin.Capability{ok(), c}}

	v, err := Run(context.Background(), p, "demo.item.show", map[string]any{"name": "a"}, true)
	if err != nil || v == nil {
		t.Fatalf("Run = %v, %v; a nil *view.Error is the handler's answer", v, err)
	}
	if len(saw) != 1 || saw[0].Int("limit") != 10 || saw[0].String("name") != "a" ||
		saw[0].Surface() != plugin.SurfaceCLI || !saw[0].DryRun {
		t.Fatalf("the handler was not handed what rta hands it: %+v", saw)
	}

	for _, values := range []map[string]any{{}, {"name": "a", "limit": 0}} {
		if _, err := Run(context.Background(), p, "demo.item.show", values, false); err == nil {
			t.Errorf("Run(%v) was not refused as rta refuses it", values)
		}
	}
	if len(saw) != 1 {
		t.Errorf("a refused call reached the handler: %d calls", len(saw))
	}
	if _, err := Run(context.Background(), p, "demo.item.shwo", nil, false); err == nil ||
		!strings.Contains(err.Error(), `no capability "demo.item.shwo"`) {
		t.Errorf("a capability that is not there was not said to be: %v", err)
	}
}
