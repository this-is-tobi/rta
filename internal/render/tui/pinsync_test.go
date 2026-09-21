package tui

import (
	"sort"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
)

// What the refresh tick does about the pinned profiles: one read of the
// config for all of them, a rebuild when one of them changed, and no second
// bind for the one that is also the switched-on environment.

// A pin to the switched-on environment rides on the environment's own
// bind: the default-instance panel of a following tile that expanded is
// pinned to exactly the profile switched on, and one landing marks both,
// so the store is unlocked once per switch rather than twice.
func TestAPinToTheSwitchedOnEnvironmentRidesOnItsBind(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{})
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	msgs := unbatch(m.syncActive())
	var names []string
	for _, msg := range msgs {
		if b, ok := msg.(boundMsg); ok {
			names = append(names, b.name)
		}
	}
	sort.Strings(names)
	if got := strings.Join(names, " "); got != "prod prod/analytics" {
		t.Fatalf("binds started = %q, want the environment's and the analytics pin's, and no second one for prod", got)
	}
	if pin := m.pins["prod"]; pin.ready {
		t.Fatalf("the prod pin = %+v, want it noted and waiting on the environment's landing", pin)
	}
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	if pin := m.pins["prod"]; !pin.ready || pin.bound == nil {
		t.Errorf("the prod pin was not marked by the environment's landing: %+v", pin)
	}
	if tc := m.connFor(m.tiles[1]); tc.pending || tc.err != nil || tc.filled["host"] != "prod.internal" {
		t.Errorf("the default panel runs against %+v, want prod's default host", tc)
	}
}

// A pinned profile that gains a connection grows its panels on the next
// tick, the way the switched-on environment does: a connection added later
// gets its panel, and it should not take a restart.
func TestAPinnedProfileGainingAConnectionGrowsItsPanels(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.status@prod" {
		t.Fatalf("tiles = %q", got)
	}
	cfg := savedConfig(t)
	cfg.Profiles["prod"].Plugins["db/analytics"] = conn(map[string]any{"host": "analytics.prod.internal"})
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	cmd := m.syncPins()
	m = land(t, m, cmd)
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.status@prod db.status@prod/analytics" {
		t.Fatalf("after the edit, tiles = %q, want the new connection's panel", got)
	}
	if tc := m.connFor(m.tiles[3]); tc.pending || tc.err != nil || tc.filled["host"] != "analytics.prod.internal" {
		t.Errorf("the new panel runs against %+v, want the connection just added", tc)
	}
}
