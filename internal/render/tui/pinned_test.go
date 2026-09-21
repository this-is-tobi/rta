package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Tiles a person adds to the automatic set, and tiles pinned to a profile.
//
// The automatic dashboard leaves out everything that reaches off the box,
// and `tiles:` was the only way to get one of those on screen — at the price
// of freezing the whole dashboard. `add:` joins entries to the automatic set
// instead, and an entry's `profile:` pins it to one connection, which is
// what lets one capability sit on the dashboard twice, once per cluster.

func tileKeys(tiles []tile) []string {
	out := make([]string, 0, len(tiles))
	for _, t := range tiles[1:] {
		out = append(out, t.key())
	}
	return out
}

func TestAddedTilesJoinTheAutomaticSetWithoutFreezingIt(t *testing.T) {
	reg := multiRegistry(t)
	tiles := buildTiles(reg, config.Dashboard{Add: []config.Tile{
		{ID: "alpha.info", Profile: "prod"},
		{ID: "alpha.info", Profile: "staging"},
	}})
	got := tileKeys(tiles)
	want := []string{"alpha.info", "beta.info", "alpha.info@prod", "alpha.info@staging"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("tiles = %v, want the automatic set then the added ones: %v", got, want)
	}
	if tiles[1].source != tileAuto || tiles[3].source != tileAdded {
		t.Errorf("sources = %v/%v, want automatic then added", tiles[1].source, tiles[3].source)
	}
	if tiles[3].profile != "prod" || tiles[4].profile != "staging" {
		t.Errorf("profiles = %q/%q, want prod then staging", tiles[3].profile, tiles[4].profile)
	}
}

// An entry that is not a read is refused on the added list exactly as it
// is on the stated one: a tile runs on a timer with no confirmation.
func TestAnAddedTileMustStillBeARead(t *testing.T) {
	reg := multiRegistry(t)
	ok := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil }
	if err := reg.Register(plugin.Plugin{Name: "demo", Summary: "d", Capabilities: []plugin.Capability{
		{ID: "demo.rm", Summary: "remove", Safety: plugin.Destructive, Run: ok},
	}}); err != nil {
		t.Fatal(err)
	}
	for _, key := range tileKeys(buildTiles(reg, config.Dashboard{Add: []config.Tile{{ID: "demo.rm"}}})) {
		if key == "demo.rm" {
			t.Fatal("a destructive capability became a tile through add:")
		}
	}
}

// order: names a pinned tile by its key, so it can lead on its own.
func TestOrderPlacesAPinnedTileByItsKey(t *testing.T) {
	tiles := buildTiles(multiRegistry(t), config.Dashboard{
		Add:   []config.Tile{{ID: "alpha.info", Profile: "prod"}},
		Order: []string{"alpha.info@prod"},
	})
	if got := tileKeys(tiles); got[0] != "alpha.info@prod" || got[1] != "alpha.info" {
		t.Errorf("order = %v, want the pinned tile first and the automatic one after it", got)
	}
}

// hidden: is about the automatic set. An added entry is the person's own
// ask, and a stale hidden line must not take it down.
func TestHiddenDoesNotTakeDownAnAddedTile(t *testing.T) {
	tiles := buildTiles(multiRegistry(t), config.Dashboard{
		Hidden: []string{"alpha.info"},
		Add:    []config.Tile{{ID: "alpha.info", Profile: "prod"}},
	})
	if got := strings.Join(tileKeys(tiles), " "); got != "beta.info alpha.info@prod" {
		t.Errorf("tiles = %q, want the automatic alpha hidden and the added one kept", got)
	}
}

// A stated list keeps its own order; the added entries follow it, and a
// move across the seam is an end rather than an order the file could not
// reproduce.
func TestAStatedListKeepsAddedEntriesAfterIt(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	dash := config.Dashboard{
		Tiles: []config.Tile{{ID: "beta.info"}},
		Add:   []config.Tile{{ID: "alpha.info", Profile: "prod"}},
	}
	m := New(multiRegistry(t), dash, nil)
	if got := strings.Join(tileKeys(m.tiles), " "); got != "beta.info alpha.info@prod" {
		t.Fatalf("tiles = %q", got)
	}
	m.selected = 2
	if note := m.moveSelected(-1); note != "" || m.tiles[2].key() != "alpha.info@prod" {
		t.Errorf("an added tile crossed into the stated list: %q / %v", note, tileKeys(m.tiles))
	}
}

func pinnedModel(t *testing.T, cfg config.Config, dash config.Dashboard, plugins ...plugin.Plugin) Model {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	cfg.Dashboard = dash
	if err := config.Write(cfg); err != nil {
		t.Fatal(err)
	}
	reg := registry.New()
	for _, p := range append([]plugin.Plugin{dbPlugin()}, plugins...) {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	m := New(reg, dash, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return sized.(Model)
}

// H on an added tile withdraws the entry rather than hiding the capability
// — hiding by ID would take down both kube tiles — and the note says the
// command that puts it back.
func TestRemovingAnAddedTileWithdrawsItsEntry(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{
		{ID: "db.status", Profile: "prod"},
		{ID: "db.status", Profile: "staging"},
	}})
	keys := tileKeys(m.tiles)
	m.selected = len(m.tiles) - 2 // db.status@prod
	if m.tiles[m.selected].key() != "db.status@prod" {
		t.Fatalf("tiles = %v", keys)
	}
	note := m.hideSelected()
	if !strings.Contains(note, "rta dashboard add db.status --profile prod") {
		t.Errorf("note = %q, want the command that puts it back", note)
	}
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Add) != 1 || cfg.Dashboard.Add[0].Profile != "staging" {
		t.Errorf("saved add = %v, want only the staging entry left", cfg.Dashboard.Add)
	}
	if len(cfg.Dashboard.Hidden) != 0 {
		t.Errorf("hidden = %v, want nothing — the automatic db tile was not the one taken down", cfg.Dashboard.Hidden)
	}
	if got := strings.Join(tileKeys(m.tiles), " "); strings.Contains(got, "db.status@prod") {
		t.Errorf("the withdrawn tile is still on screen: %q", got)
	}
}

// A move records keys, so a pinned tile keeps its place on the next run,
// and the written list follows the screen.
func TestAMoveRecordsTileKeysAndReordersTheAddedList(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{
		{ID: "db.status", Profile: "prod"},
		{ID: "db.status", Profile: "staging"},
	}})
	m.selected = len(m.tiles) - 1 // db.status@staging
	if note := m.moveSelected(-1); note == "" {
		t.Fatal("the move was refused")
	}
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Order) != 3 || cfg.Dashboard.Order[1] != "db.status@staging" || cfg.Dashboard.Order[2] != "db.status@prod" {
		t.Errorf("saved order = %v, want keys with staging ahead of prod", cfg.Dashboard.Order)
	}
	if cfg.Dashboard.Add[0].Profile != "staging" || cfg.Dashboard.Add[1].Profile != "prod" {
		t.Errorf("saved add = %v, want the list in the order the screen shows", cfg.Dashboard.Add)
	}
	if got := tileKeys(buildTiles(m.reg, cfg.Dashboard)); got[1] != "db.status@staging" {
		t.Errorf("reloaded = %v, want the move kept", got)
	}
}

// Two entries sharing a key are one tile to a move, and both survive it: an
// index by key alone kept one and lost the other.
func TestReorderTilesKeepsEntriesSharingAKey(t *testing.T) {
	got := reorderTiles([]config.Tile{
		{ID: "obj.get", With: map[string]any{"host": "a"}},
		{ID: "obj.get", With: map[string]any{"host": "b"}},
		{ID: "sys.cpu"},
	}, []string{"sys.cpu", "obj.get"})
	if len(got) != 3 || got[0].ID != "sys.cpu" || got[1].With["host"] != "a" || got[2].With["host"] != "b" {
		t.Errorf("reordered = %v, want sys.cpu then both obj.get entries in their own order", got)
	}
}

// Enter on a pinned tile opens that profile, through the form's own picker.
func TestRunValuesCarryThePinnedProfile(t *testing.T) {
	pinned := tile{cap: plugin.Capability{ID: "db.status"}, profile: "prod", values: map[string]any{"schema": "public"}}
	got := pinned.runValues()
	if got[profileInput] != "prod" || got["schema"] != "public" {
		t.Errorf("runValues = %v, want the inputs plus the profile under the picker key", got)
	}
	if _, leaked := pinned.values[profileInput]; leaked {
		t.Error("runValues wrote the picker key into the tile's own values")
	}
	following := tile{cap: plugin.Capability{ID: "db.status"}, values: map[string]any{"schema": "public"}}
	if got := following.runValues(); len(got) != 1 {
		t.Errorf("an unpinned tile's run values = %v, want its inputs untouched", got)
	}
}

// A pinned tile runs against its own profile whatever is switched on, and
// is left alone — "loading…", not an error — until that profile is bound.
func TestAPinnedTileRunsAgainstItsOwnProfile(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	pinned := m.tiles[len(m.tiles)-1]
	if pinned.key() != "db.status@prod" {
		t.Fatalf("tiles = %v", tileKeys(m.tiles))
	}
	pin, noted := m.pins["prod"]
	if !noted || pin.ready {
		t.Fatalf("New noted pins = %v, want prod noted and not yet bound", m.pins)
	}
	if tc := m.connFor(pinned); !tc.pending {
		t.Errorf("before the bind: %+v, want pending", tc)
	}
	// The automatic db tile follows the switch, which is off.
	if tc := m.connFor(m.tiles[1]); tc.name != "" || tc.pending {
		t.Errorf("the following tile = %+v, want the base configuration", tc)
	}

	msg, ok := bindCmd(m.reg, "prod", pin.stamp)().(boundMsg)
	if !ok {
		t.Fatal("bindCmd did not produce a boundMsg")
	}
	after, _ := m.Update(msg)
	m = after.(Model)
	tc := m.connFor(m.tiles[len(m.tiles)-1])
	if tc.pending || tc.err != nil || tc.name != "prod" || tc.filled["host"] != "prod.internal" {
		t.Errorf("after the bind: %+v, want prod's own host", tc)
	}
}

// A bind landing for a pin re-runs the pinned tiles whatever pace they
// declared, and leaves the others inside theirs: a tile about prod is
// about prod whatever was switched, and the reverse holds too.
func TestABindLandingForAPinResetsOnlyItsTiles(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "prod"}}})
	fired := time.Now().Add(-time.Minute)
	for i := range m.tiles {
		m.tiles[i].cap.Refresh = time.Hour
		m.tiles[i].lastFired = fired
	}
	pin := m.pins["prod"]
	after, _ := m.Update(boundMsg{name: "prod", stamp: pin.stamp, bound: map[string]envBind{}})
	m = after.(Model)
	if !m.tiles[len(m.tiles)-1].lastFired.After(fired) {
		t.Error("the pinned tile was not re-run when its profile landed")
	}
	if !m.tiles[1].lastFired.Equal(fired) {
		t.Error("the automatic tile was re-run inside its pace for a profile it does not follow")
	}
	if !m.pins["prod"].ready {
		t.Error("the pin was not marked bound")
	}
}

// A profile that is not in the file is a fact about the config, said on the
// tile, never a silent run against the base configuration.
func TestAPinnedProfileThatDoesNotExistIsSaidOnTheTile(t *testing.T) {
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{{ID: "db.status", Profile: "ghost"}}})
	tc := m.connFor(m.tiles[len(m.tiles)-1])
	if tc.pending || tc.err == nil || tc.err.Code != "core.profile.unknown" {
		t.Errorf("connFor = %+v, want core.profile.unknown", tc)
	}
	if !strings.Contains(tc.err.Hint, "rta dashboard rm db.status --profile ghost") {
		t.Errorf("hint = %q, want the command that takes the tile down", tc.err.Hint)
	}
}

// A profile silent about the tile's plugin is an error on the tile — the
// title would name prod while the numbers came from localhost.
func TestAPinnedProfileSilentAboutThePluginIsAnError(t *testing.T) {
	other := plugin.Plugin{Name: "other", Summary: "o", Capabilities: []plugin.Capability{{
		ID: "other.info", Summary: "i", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Config: "host"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}}}
	m := pinnedModel(t, twoProfileConfig(), config.Dashboard{Add: []config.Tile{{ID: "other.info", Profile: "prod"}}}, other)
	pin := m.pins["prod"]
	msg := bindCmd(m.reg, "prod", pin.stamp)().(boundMsg)
	after, _ := m.Update(msg)
	m = after.(Model)
	tc := m.connFor(m.tiles[len(m.tiles)-1])
	if tc.err == nil || tc.err.Code != "tui.tile.profile" || !strings.Contains(tc.err.Message, "says nothing about other") {
		t.Errorf("connFor = %+v, want tui.tile.profile naming the plugin", tc)
	}
}

// The title names the profile beside the capability, since two tiles of one
// capability are otherwise the same panel twice.
func TestTheTitleNamesThePinnedProfile(t *testing.T) {
	pinned := tile{cap: plugin.Capability{ID: "db.status"}, profile: "prod", view: view.Text{Body: "ok"}}
	top := strings.SplitN(plain(renderTile(pinned, 60, 6, false)), "\n", 2)[0]
	if !strings.Contains(top, "db.status") || !strings.Contains(top, "prod") {
		t.Errorf("title line = %q, want the capability and the profile", top)
	}
	following := tile{cap: plugin.Capability{ID: "db.status"}, view: view.Text{Body: "ok"}}
	if top := strings.SplitN(plain(renderTile(following, 60, 6, false)), "\n", 2)[0]; strings.Contains(top, "prod") {
		t.Errorf("an unpinned tile named a profile: %q", top)
	}
}

// Layout is `rta dashboard list`'s view of the arrangement.
func TestLayoutReportsEveryTileAndWhereItCameFrom(t *testing.T) {
	got := Layout(multiRegistry(t), config.Dashboard{Add: []config.Tile{{ID: "alpha.info", Profile: "prod"}}})
	if len(got) != 3 || got[0].Source != "automatic" || got[2].Source != "added" || got[2].Profile != "prod" {
		t.Errorf("layout = %+v", got)
	}
}
