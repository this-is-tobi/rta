package pluginconf

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One key read by capabilities that bound it differently, the shape net's
// `timeout` has: six capabilities, three maxima. Check kept one field per
// key, the last declared, and held nothing to a range at all, so doctor said
// "plugin config ok" about a value no capability could run with as written.
func sharedRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "sys", Summary: "sys", Capabilities: []plugin.Capability{
			{ID: "sys.ping", Summary: "ping", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "timeout", Type: plugin.Int, Default: 10, Min: 1, Max: 300, Config: "timeout"},
				{Name: "mode", Type: plugin.String, Config: "mode", Options: []string{"fast", "safe"}},
			}},
			{ID: "sys.port", Summary: "port", Safety: plugin.Read, Run: run, Inputs: []plugin.Field{
				{Name: "timeout", Type: plugin.Int, Default: 2, Min: 1, Max: 60, Config: "timeout"},
				{Name: "mode", Type: plugin.String, Config: "mode", Options: []string{"safe", "thorough"}},
			}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func sharedCheck(t *testing.T, values map[string]any) []Problem {
	t.Helper()
	r, _ := Resolve(trusted(t, config.Config{Plugins: map[string]map[string]any{"sys": values}}), installed)
	return r.Check(sharedRegistry(t))
}

func TestAKeyIsReportedOnlyWhenNoCapabilityReadingItAcceptsTheValue(t *testing.T) {
	// Inside some reader's range, and naming some reader's option: every
	// capability runs with it (the rest hold the number to their own range).
	for _, values := range []map[string]any{
		{"timeout": 90}, {"timeout": 300}, {"mode": "thorough"}, {"mode": "fast"},
	} {
		if problems := sharedCheck(t, values); len(problems) != 0 {
			t.Errorf("%v: reported %v", values, problems)
		}
	}
	for _, tc := range []struct {
		values map[string]any
		hint   string
	}{
		{map[string]any{"timeout": 0}, "from 1 to 300"},
		{map[string]any{"timeout": 400}, "from 1 to 300"},
		{map[string]any{"mode": "reckless"}, "fast, safe, thorough"},
	} {
		problems := sharedCheck(t, tc.values)
		if len(problems) != 1 || !strings.Contains(problems[0].Hint, tc.hint) {
			t.Errorf("%v: %v, want one problem hinting %q", tc.values, problems, tc.hint)
		}
	}
}

// The host runs an option written in another case as the declared one, and
// doctor called it invalid: `encoding: BASE32` printed a base32 token and
// was reported as not a value encoding accepts.
func TestAnOptionInAnotherCaseIsNotReported(t *testing.T) {
	if problems := checkOf(t, map[string]any{"mode": "SAFE"}); len(problems) != 0 {
		t.Errorf("a case variant was reported: %v", problems)
	}
}

func TestSharedFieldIsTheWidestOfItsReaders(t *testing.T) {
	got := SharedField([]plugin.Field{
		{Name: "timeout", Type: plugin.Int, Min: 1, Max: 60, Required: true},
		{Name: "timeout", Type: plugin.Int, Min: 0, Max: 300, Required: true},
		{Name: "timeout", Type: plugin.Int, Min: 2, Max: 120, Required: true},
	})
	if got.Min != 0 || got.Max != 300 || !got.Required {
		t.Errorf("merged = min %v max %v required %v", got.Min, got.Max, got.Required)
	}
	got = SharedField([]plugin.Field{
		{Name: "mode", Type: plugin.String, Options: []string{"fast", "safe"}},
		{Name: "mode", Type: plugin.String, Options: []string{"safe", "thorough"}},
		{Name: "mode", Type: plugin.String, Options: []string{"Fast", "careful"}},
	})
	if !reflect.DeepEqual(got.Options, []string{"fast", "safe", "thorough", "careful"}) {
		t.Errorf("merged options %v", got.Options)
	}
	// A reader with no bound, no options or no requirement loosens the key
	// the same way: some capability accepts what it would.
	got = SharedField([]plugin.Field{
		{Name: "limit", Type: plugin.Int, Min: 1, Max: 60, Required: true},
		{Name: "limit", Type: plugin.Int, Max: 10},
	})
	if got.Min != nil || got.Max != 60 || got.Required {
		t.Errorf("merged = min %v max %v required %v", got.Min, got.Max, got.Required)
	}
	got = SharedField([]plugin.Field{
		{Name: "mode", Type: plugin.String, Options: []string{"fast"}},
		{Name: "mode", Type: plugin.String},
	})
	if got.Options != nil {
		t.Errorf("merged options %v, want none beside a free-text reader", got.Options)
	}
}
