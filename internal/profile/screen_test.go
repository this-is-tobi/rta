package profile

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A profile that writes its screen in its entry and has a view of the same name
// beside it is told, and is still an environment that resolves: nothing about
// its connections is in question, and the dashboard: block draws until the two
// statements are one.
func TestAScreenThatCannotBeDrawnIsNotedAndNeverRefusesTheProfile(t *testing.T) {
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{
		{ID: "db.status", Summary: "status", Safety: plugin.Read, Run: run,
			Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host", Local: true}}},
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := load(t, `dashboard:
  views:
    staging:
      columns: 5
profiles:
  staging:
    plugins:
      db:
        set:
          host: s.example
    dashboard:
      columns: 3
  default:
    plugins:
      db:
        set:
          host: d.example
    dashboard:
      columns: 3
  shop-prod:
    plugins:
      db:
        set:
          host: p.example
    dashboard:
      columns: 3
  ops:
    plugins:
      db:
        set:
          host: o.example
    dashboard: staging
`)
	if problems := Check(cfg, reg); len(problems) != 0 {
		t.Fatalf("a screen that cannot be drawn refused a profile: %v", problems)
	}
	for _, c := range reg.Capabilities() {
		for _, name := range []string{"staging", "default", "shop-prod", "ops"} {
			if _, verr := Lookup(cfg, c, name, reg); verr != nil {
				t.Errorf("%s: %s refused the profile: %s", name, c.ID, verr.Message)
			}
		}
	}
	got := map[string]Problem{}
	for _, n := range Notes(cfg, reg) {
		got[n.Name] = n
	}
	if len(got) != 2 {
		t.Errorf("notes = %+v, want staging's and default's alone: shop-prod's screen is drawn, and ops selects a view", got)
	}
	staging := got["staging"]
	for _, want := range []string{"writes its dashboard in its entry", "a view called staging", "neither is drawn"} {
		if !strings.Contains(staging.Reason, want) {
			t.Errorf("staging's note lacks %q: %q", want, staging.Reason)
		}
	}
	if !strings.Contains(staging.Hint, "keep one") {
		t.Errorf("staging's hint = %q", staging.Hint)
	}
	if reason := got["default"].Reason; !strings.Contains(reason, "`default` names the `dashboard:` block") {
		t.Errorf("default's note = %q", reason)
	}
}
