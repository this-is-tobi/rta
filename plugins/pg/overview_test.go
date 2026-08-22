package main

import (
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The floor is the whole content of the verdict — get it backwards and every
// database with a healthy cache reads as one that needs tuning, and vice
// versa. Boundary-tested rather than sampled, the same reason usageStatus and
// loadVerdict are in builtin/sys: a >= that should have been a > fails
// exactly at the number somebody is staring at.
func TestCacheVerdictNamesLowRatiosAndStaysQuietAboveTheFloor(t *testing.T) {
	cases := []struct {
		ratio float64
		low   bool
	}{
		{0, true},
		{cacheHitFloor - 0.01, true},
		{cacheHitFloor, false}, // at the floor is healthy, not low
		{cacheHitFloor + 0.01, false},
		{100, false},
	}
	for _, tc := range cases {
		got := cacheVerdict(tc.ratio)
		isLow := got != "healthy"
		if isLow != tc.low {
			t.Errorf("cacheVerdict(%.2f) = %q, want low=%v", tc.ratio, got, tc.low)
		}
	}
}

// pg.overview declares Detailed for a compact and a full form. Checked
// directly on the literal: cap() cannot rescue a missing Detailed the way it
// rescues a missing NoPreview below, so this is the one property that lives
// only where it is declared.
func TestOverviewIsDetailed(t *testing.T) {
	if !findCapability(t, "pg.overview").Detailed {
		t.Error("pg.overview does not declare Detailed — no compact/full split")
	}
}

// Every capability in this plugin opens a real connection, and ADR 0018 §7
// measured that connection killing a kubectl port-forward on its own clean
// disconnect — so cap() forces NoPreview on all five, unconditionally,
// rather than trust each declaration to remember it. That override is what
// this actually guards: pg.overview's own literal could say NoPreview:
// false and cap() would still correct it, so the mutation worth catching is
// cap() itself losing the line, which would put every capability here back
// on the automatic dashboard's five-second timer at once.
func TestEveryCapabilityStaysOffTheAutomaticDashboard(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s does not declare NoPreview — the automatic dashboard would "+
				"poll a real database connection every few seconds", c.ID)
		}
	}
}

// Every capability here shares one connection contract (conn.go's cap()
// helper appends it), and overview is not exempt just because it composes
// the others rather than running its own query first.
func TestOverviewDeclaresTheSharedConnectionFields(t *testing.T) {
	c := findCapability(t, "pg.overview")
	want := []string{"host", "port", "user", "database", "sslmode", "password"}
	have := map[string]bool{}
	for _, f := range c.Inputs {
		have[f.Name] = true
	}
	for _, name := range want {
		if !have[name] {
			t.Errorf("pg.overview has no %q input — cap() was not applied", name)
		}
	}
}

func findCapability(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}
