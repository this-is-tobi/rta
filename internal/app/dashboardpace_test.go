package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `refresh:` is one key at three levels — a tile, a section, the screen — and
// means the same at each: the nearest one wins, a default yields to a capability
// that declared a slower pace, and a demand that cannot be met is said.

// paceRegistry is the db plugin and one capability that declared a two-hour pace,
// the way eol.watch does.
func paceRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := setRegistry(t)
	if err := reg.Register(plugin.Plugin{
		Name: "slow", Summary: "a slow plugin",
		Capabilities: []plugin.Capability{{
			ID: "slow.watch", Summary: "watch", Safety: plugin.Read, Refresh: 2 * time.Hour,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
		}},
	}); err != nil {
		t.Fatal(err)
	}
	return reg
}

func paceOf(t *testing.T, csv, tileAndProfile string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(csv), "\n") {
		if strings.HasPrefix(line, tileAndProfile+",") {
			return line
		}
	}
	t.Fatalf("no row for %q in:\n%s", tileAndProfile, csv)
	return ""
}

func TestDashboardAddRefreshWritesTheEntryAndListSaysItWasStatedOnTheTile(t *testing.T) {
	run := session(t, paceRegistry(t))
	if _, errOut, err := run("profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	out, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod", "--refresh", "90s")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if !strings.Contains(out, "every 1m30s (the tile's)") {
		t.Errorf("the receipt does not say the pace and where it came from:\n%s", out)
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 1 || add[0].Refresh != "1m30s" {
		t.Fatalf("add = %+v, want the pace written the way a duration prints", add)
	}
	csv, _, err := run("dashboard", "list", "-o", "csv")
	if err != nil {
		t.Fatal(err)
	}
	if row := paceOf(t, csv, "db.status,prod"); !strings.Contains(row, "every 1m30s (tile)") {
		t.Errorf("list does not say the tile's pace: %s", row)
	}

	// Without it the entry is back to what its capability gets: the pace of one
	// that reaches a connection, and no place stated.
	if _, errOut, err := run("dashboard", "add", "db.status", "--profile", "prod"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	csv, _, _ = run("dashboard", "list", "-o", "csv")
	if row := paceOf(t, csv, "db.status,prod"); !strings.Contains(row, "every 30s") || strings.Contains(row, "(tile)") {
		t.Errorf("an entry with no refresh does not run at the pace of a connection: %s", row)
	}
}

func TestDashboardAddRefusesAPaceNoTileCanRunAt(t *testing.T) {
	run := session(t, paceRegistry(t))
	for _, tc := range []struct {
		name, capability, refresh, code string
	}{
		{"no unit", "slow.watch", "30", "core.dashboard.refresh"},
		{"not a time", "slow.watch", "soon", "core.dashboard.refresh"},
		{"more than a day", "slow.watch", "48h", "core.dashboard.refresh"},
		{"under the timer's tick", "db.status", "1s", "core.dashboard.refresh.floor"},
		{"under a declared pace", "slow.watch", "10m", "core.dashboard.refresh.floor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, errOut, err := run("dashboard", "add", tc.capability, "--refresh", tc.refresh)
			if err == nil || !strings.Contains(errOut, tc.code) {
				t.Fatalf("--refresh %s on %s: %v\n%s", tc.refresh, tc.capability, err, errOut)
			}
		})
	}
	if add := loadedConfig(t).Dashboard.Add; len(add) != 0 {
		t.Errorf("a refused add wrote %+v", add)
	}
	// A slower pace than the declared one is the one thing a configuration may do.
	if _, errOut, err := run("dashboard", "add", "slow.watch", "--refresh", "6h"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
}

const paceDropIn = `dashboard:
  refresh: 1m
  sections:
    databases:
      refresh: 45s
  add:
  - id: db.status
    profile: prod
    section: databases
  - id: db.status
    profile: staging
    section: databases
    refresh: 20s
  - id: db.status
    profile: other
  - id: slow.watch
    section: databases
`

func TestTheNearestRefreshWinsAndADefaultYieldsToADeclaredPace(t *testing.T) {
	run := session(t, paceRegistry(t))
	for _, args := range [][]string{
		{"profile", "set", "prod", "--plugin", "db", "--set", "host=prod.internal"},
		{"profile", "set", "staging", "--plugin", "db", "--set", "host=staging.internal"},
		{"profile", "set", "other", "--plugin", "db", "--set", "host=other.internal"},
	} {
		if _, errOut, err := run(args...); err != nil {
			t.Fatalf("%v %q", err, errOut)
		}
	}
	dropIn(t, "10-pace.yaml", paceDropIn)
	csv, errOut, err := run("dashboard", "list", "-o", "csv")
	if err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	for tile, want := range map[string]string{
		"db.status,prod":    "every 45s (section)",
		"db.status,staging": "every 20s (tile)",
		"db.status,other":   "every 1m (screen)",
		// The section's 45s is a default, and this capability said two hours.
		"slow.watch,": "every 2h",
	} {
		row := paceOf(t, csv, tile)
		if !strings.Contains(row, want) {
			t.Errorf("%s: %q does not say %q", tile, row, want)
		}
		if tile == "slow.watch," && strings.Contains(row, "(") && strings.Contains(row, "(section)") {
			t.Errorf("a default that yielded is reported as stated: %s", row)
		}
	}
	sections, _, err := run("dashboard", "sections", "-o", "csv")
	if err != nil || !strings.Contains(sections, "45s") {
		t.Errorf("sections does not say the section's refresh: %v\n%s", err, sections)
	}
	if _, errOut, err := run("config", "check"); err != nil {
		t.Errorf("a default under a declared pace was reported: %v\n%s", err, errOut)
	}
}

func TestConfigCheckSaysWhichRefreshCannotBeMetAndWhatTheTileDoesInstead(t *testing.T) {
	run := session(t, paceRegistry(t))
	dropIn(t, "10-bad.yaml", `dashboard:
  refresh: 30
  sections:
    databases:
      refresh: soon
  add:
  - id: slow.watch
    refresh: 10m
  - id: db.status
    refresh: 2s
  - id: db.status
    profile: x
    refresh: 1m
`)
	out, errOut, err := run("config", "check", "-o", "json")
	if err == nil {
		t.Fatalf("a configuration with refresh problems passed:\n%s", out)
	}
	text := out + errOut
	for _, want := range []string{
		"dashboard.refresh", `\"30\" is not a length of time`,
		"dashboard.sections.databases.refresh", `\"soon\"`,
		"dashboard.add[0].refresh", "no more often than every 2h",
		"dashboard.add[1].refresh", "no more often than every 5s",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the check does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "dashboard.add[2].refresh") {
		t.Errorf("the check names a pace a tile can run at:\n%s", text)
	}
}

func TestConfigSetDashboardRefreshIsTheScreensDefault(t *testing.T) {
	run := session(t, paceRegistry(t))
	if _, errOut, err := run("config", "set", "dashboard.refresh", "90s"); err != nil {
		t.Fatalf("%v %q", err, errOut)
	}
	if got := loadedConfig(t).Dashboard.Refresh; got != "1m30s" {
		t.Errorf("dashboard.refresh = %q", got)
	}
	if _, errOut, err := run("config", "set", "dashboard.refresh", "90"); err == nil {
		t.Errorf("a pace with no unit was set:\n%s", errOut)
	}
}
