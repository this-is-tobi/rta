package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/view"
)

// One profile, several connections to one plugin. A tile whose profile
// names no instance becomes one panel per connection — the answer to
// "ohmlab holds cnpg/gitea, cnpg/keycloak and three more, and I want to
// glance at all of them" — and a tile following the switch expands into
// whatever environment is on, and collapses when nothing is.

// twoInstanceConfig: prod holds a default db and db/analytics; staging
// holds one db.
func twoInstanceConfig() config.Config {
	return config.Config{Profiles: map[string]config.Profile{
		"prod": {Plugins: map[string]config.Connection{
			"db":           conn(map[string]any{"host": "prod.internal"}),
			"db/analytics": conn(map[string]any{"host": "analytics.prod.internal"}),
		}},
		"staging": {Plugins: map[string]config.Connection{
			"db": conn(map[string]any{"host": "staging.internal"}),
		}},
	}}
}

// land runs whatever syncActive returned — one bind, or a batch of the
// environment's and the pins', which is itself a batch — and hands each
// landing to Update, the way the runtime would.
func land(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for _, msg := range unbatch(cmd) {
		next, _ := m.Update(msg)
		m = next.(Model)
	}
	return m
}

func unbatch(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{msg}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, unbatch(c)...)
	}
	return out
}

func TestAPinnedTileExpandsIntoTheProfilesConnections(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	got := strings.Join(tileKeys(m.tiles), " ")
	if got != "db.status db.status@prod db.status@prod/analytics" {
		t.Fatalf("tiles = %q, want the automatic tile then one panel per prod connection", got)
	}
	for _, tl := range m.tiles[2:] {
		if !tl.expanded || tl.source != tileAdded {
			t.Errorf("%s: expanded=%v source=%v, want an expanded added panel", tl.key(), tl.expanded, tl.source)
		}
	}
	if m.tiles[1].expanded {
		t.Error("the automatic tile, following a switch that is off, was marked expanded")
	}
}

func TestAnInstanceNamedIsOnePanel(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod/analytics"}}})
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.status@prod/analytics" {
		t.Errorf("tiles = %q, want the named connection alone", got)
	}
	if m.tiles[2].expanded {
		t.Error("a tile naming its instance was marked expanded")
	}
}

func TestAProfileWithOneConnectionStaysOnePanel(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "staging"}}})
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.status@staging" {
		t.Errorf("tiles = %q, want one panel, named as the profile", got)
	}
}

// The automatic db tile follows the switch: under prod it is prod's two
// connections, each bound through its own pin and named on its panel; with
// the switch off it is one panel again.
func TestAFollowingTileExpandsIntoTheSwitchedOnEnvironmentAndBack(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{})
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status" {
		t.Fatalf("tiles = %q", got)
	}
	if verr := profile.SaveSelection(profile.Selection{Active: "prod"}); verr != nil {
		t.Fatal(verr)
	}
	cmd := m.syncActive()
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status@prod db.status@prod/analytics" {
		t.Fatalf("under prod, tiles = %q, want one panel per connection", got)
	}
	m = land(t, m, cmd)
	analytics := m.tiles[2]
	if tc := m.connFor(analytics); tc.pending || tc.err != nil || tc.filled["host"] != "analytics.prod.internal" {
		t.Errorf("the analytics panel runs against %+v, want prod's analytics host", tc)
	}
	if got := analytics.runValues()[profileInput]; got != "prod/analytics" {
		t.Errorf("enter on the panel opens %v, want prod/analytics", got)
	}

	if verr := profile.SaveSelection(profile.Selection{}); verr != nil {
		t.Fatal(verr)
	}
	m = land(t, m, m.syncActive())
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status" {
		t.Errorf("switched off, tiles = %q, want the one following panel back", got)
	}
	if len(m.pins) != 0 {
		t.Errorf("pins = %v, want the expanded panels' pins forgotten", m.pins)
	}
}

// H on an expanded panel hides that connection's panel by its key: the
// siblings stay, the entry stays, and the note says the way back.
func TestHOnAnExpandedPanelHidesItsKeyAndKeepsItsSiblings(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	m.selected = 3 // db.status@prod/analytics
	if m.tiles[m.selected].key() != "db.status@prod/analytics" {
		t.Fatalf("tiles = %v", tileKeys(m.tiles))
	}
	note := m.hideSelected()
	if !strings.Contains(note, "rta dashboard unhide db.status --profile prod/analytics") {
		t.Errorf("note = %q, want the way back", note)
	}
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Hidden) != 1 || cfg.Dashboard.Hidden[0] != "db.status@prod/analytics" {
		t.Errorf("hidden = %v, want the panel's key", cfg.Dashboard.Hidden)
	}
	if len(cfg.Dashboard.Add) != 1 {
		t.Errorf("add = %v, want the entry kept", cfg.Dashboard.Add)
	}
	m.rebuildTiles()
	if got := strings.Join(tileKeys(m.tiles), " "); got != "db.status db.status@prod" {
		t.Errorf("rebuilt tiles = %q, want the sibling kept and the hidden panel gone", got)
	}
}

// A rebuild keeps what a panel already shows: switching environments
// must not blank the tiles that are still there.
func TestARebuildKeepsAPanelsContentByKey(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	m.tiles[2].view = view.Text{Body: "kept"}
	m.rebuildTiles()
	if body, ok := m.tiles[2].view.(view.Text); !ok || body.Body != "kept" {
		t.Errorf("the panel's content was lost in the rebuild: %+v", m.tiles[2].view)
	}
}

func TestLayoutMarksHiddenPanelsAndExpansion(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{})
	cfg := savedConfig(t)
	dash := config.Dashboard{
		Add:    []config.Tile{{ID: "db.status", Profile: "prod"}},
		Hidden: []string{"db.status@prod/analytics"},
	}
	got := Layout(m.reg, dash, InstancesOf(cfg, ""))
	if len(got) != 3 {
		t.Fatalf("layout = %+v, want the automatic tile and both panels, the hidden one included", got)
	}
	last := got[2]
	if last.Profile != "prod/analytics" || !last.Expanded || !last.Hidden {
		t.Errorf("the hidden panel = %+v, want it listed as expanded and hidden", last)
	}
	if got[1].Hidden {
		t.Errorf("its sibling = %+v, want it on screen", got[1])
	}
}
