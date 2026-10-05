package app

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A duration key is held to its range when it is written, as a number key is:
// the value stored is one a reader accepts, not one every call then moves to
// the nearest bound in silence.
func TestSetHoldsADurationKeyToTheRangeItsReadersAllow(t *testing.T) {
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "db", Summary: "db plugin",
		Capabilities: []plugin.Capability{
			{ID: "db.ping", Summary: "ping", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "wait", Type: plugin.Duration, Default: "10s", Min: "1s", Max: "5m", Config: "wait", Help: "how long"},
			}},
			{ID: "db.port", Summary: "port", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "wait", Type: plugin.Duration, Default: "2s", Min: "1s", Max: "1m", Config: "wait", Help: "how long"},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, errOut, err := runWith(t, reg, "",
		"profile", "set", "slow", "--plugin", "db", "--set", "wait=4m"); err != nil {
		t.Fatalf("a duration one reader takes was refused: %v\n%s", err, errOut)
	}
	for _, tc := range []struct{ pair, code, want string }{
		{"wait=1h", "core.profile.set.range", "wait takes a value from 1s to 5m"},
		{"wait=100ms", "core.profile.set.range", "from 1s to 5m"},
	} {
		_, errOut, err := runWith(t, reg, "", "profile", "set", "slow", "--plugin", "db", "--set", tc.pair)
		if err == nil || !strings.Contains(errOut, tc.code) || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: err = %v, output %q; want %s saying %q", tc.pair, err, errOut, tc.code, tc.want)
		}
	}
}
