package tui

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The TUI holds the values a form collected, the switched-on environment and a
// dashboard tile's own values to the fence of the connection they resolve
// through — the three places in this package that run a capability for a
// profile — before anything is fetched or opened.

func fencedStagingModel(t *testing.T, only []any) Model {
	t.Helper()
	return profileModel(t, config.Config{Profiles: map[string]config.Profile{
		"staging": {Plugins: map[string]config.Connection{"db": {
			Set:  map[string]any{"host": "staging.internal"},
			Only: map[string]any{"schema": only},
		}}},
	}})
}

func TestARunThroughAFencedEnvironmentIsRefusedBeforeItStarts(t *testing.T) {
	noHistory(t)
	m := fencedStagingModel(t, []any{"app", "app-jobs"})
	switchOn(t, &m, "staging")
	c := dbPlugin().Capabilities[1]

	for name, values := range map[string]map[string]any{
		"another value": {"schema": "prod"},
		"a blank":       {"schema": ""},
		"nothing":       {},
	} {
		_, _, _, verr := m.resolveProfile(c, values)
		if verr == nil || verr.Code != "core.profile.fenced" {
			t.Errorf("%s: resolveProfile = %v, want core.profile.fenced", name, verr)
			continue
		}
		if !strings.Contains(verr.Message, "the schema box") || !strings.Contains(verr.Message, "app, app-jobs") {
			t.Errorf("%s: message = %q, want the box and what the environment reaches", name, verr.Message)
		}
	}
	// A value inside the fence is an ordinary run, through the cached bind or not.
	if name, _, _, verr := m.resolveProfile(c, map[string]any{"schema": "app-jobs"}); verr != nil || name != "staging" {
		t.Errorf("a value inside the fence: %q, %v", name, verr)
	}
	// And the picker is a route to the same connection: naming it by hand is
	// held to the same list as living in it.
	m2 := fencedStagingModel(t, []any{"app"})
	if _, _, _, verr := m2.resolveProfile(c, map[string]any{profileInput: "staging", "schema": "prod"}); verr == nil {
		t.Error("naming the environment in the form's picker walked past its fence")
	}
}

// A one-entry list is the whole of the input, and the form opens showing it.
func TestAFormOpensOnTheOnlyValueAFenceAllows(t *testing.T) {
	noHistory(t)
	m := fencedStagingModel(t, []any{"only-this"})
	seed, derived, _ := m.formSeed(dbPlugin().Capabilities[1], nil, "staging")
	if seed["schema"] != "only-this" {
		t.Errorf("schema seeded as %v, want the one value the fence allows", seed["schema"])
	}
	if !derived["schema"] {
		t.Error("the seeded value is a display, and was not marked as one")
	}
}

// A dashboard tile is a call too, and a refused one is an error in its own
// panel, not a quiet read of somewhere the profile says it does not reach.
func TestATileThroughAFencedEnvironmentIsRefused(t *testing.T) {
	var seen string
	ti := reachedTile(t, &seen)
	ti.cap.Inputs = append(ti.cap.Inputs, plugin.Field{Name: "schema", Type: plugin.String, Config: "schema"})
	ti.values = map[string]any{"schema": "prod"}
	conn := config.Connection{Only: map[string]any{"schema": []any{"app"}}}

	msg := tileCmd(0, ti, statedConfig{}, "staging", nil, conn)().(tileMsg)
	if msg.err == nil || msg.err.Code != "core.profile.fenced" {
		t.Fatalf("the tile's error = %v, want core.profile.fenced", msg.err)
	}
	if seen != "" {
		t.Errorf("the handler ran anyway, against %s", seen)
	}
	ti.values = map[string]any{"schema": "app"}
	if msg := tileCmd(0, ti, statedConfig{}, "staging", nil, conn)().(tileMsg); msg.err != nil {
		t.Errorf("a tile inside the fence was refused: %v", msg.err)
	}
}
