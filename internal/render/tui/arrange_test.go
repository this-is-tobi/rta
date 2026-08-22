package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/builtin/all"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// multiRegistry mirrors the real shape of the built-ins: some plugins can
// show something with no configuration, others cannot do anything until they
// are told a host or a URL.
func multiRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	ready := func(ns, id string) plugin.Capability {
		return plugin.Capability{
			ID: ns + "." + id, Summary: id + " of " + ns, Safety: plugin.Read, Idempotent: true,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "LIVE-" + ns}, nil
			},
		}
	}
	// needy has a required input with no default: nothing to show until asked.
	needy := func(ns, id string) plugin.Capability {
		c := ready(ns, id)
		c.Inputs = []plugin.Field{{Name: "host", Type: plugin.String, Positional: true, Required: true, Help: "h"}}
		return c
	}
	for _, p := range []plugin.Plugin{
		{Name: "alpha", Summary: "first", Capabilities: []plugin.Capability{ready("alpha", "info")}},
		{Name: "beta", Summary: "second", Capabilities: []plugin.Capability{ready("beta", "info")}},
		// Like cert/http: every capability needs to be told what to look at.
		{Name: "zeta", Summary: "needs a target", Capabilities: []plugin.Capability{
			needy("zeta", "inspect"), needy("zeta", "watch"),
		}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// tileIDs lists the arrangement, search tile excluded.
func tileIDs(tiles []tile) []string {
	out := make([]string, 0, len(tiles))
	for _, t := range tiles[1:] {
		out = append(out, t.cap.ID)
	}
	return out
}

// TestEveryUsefulPluginGetsATile is the property: a plugin that can show
// something without being asked anything is on the landing screen, including
// one that arrived later.
func TestEveryUsefulPluginGetsATile(t *testing.T) {
	reg := multiRegistry(t)
	tiles := buildTiles(reg, config.Dashboard{})

	seen := map[string]bool{}
	for _, ti := range tiles[1:] {
		seen[strings.Split(ti.cap.ID, ".")[0]] = true
	}
	for _, name := range []string{"alpha", "beta"} {
		if !seen[name] {
			t.Errorf("plugin %q has no tile: %v", name, tileIDs(tiles))
		}
	}
}

// A plugin that cannot run anything unprompted gets no tile at all: the
// alternative is a panel showing the same error every five seconds, which
// costs a screenful of attention to say nothing.
func TestPluginWithNothingToShowGetsNoTile(t *testing.T) {
	tiles := buildTiles(multiRegistry(t), config.Dashboard{})
	for _, id := range tileIDs(tiles) {
		if strings.HasPrefix(id, "zeta") {
			t.Errorf("a plugin with no zero-config capability got a tile: %v", tileIDs(tiles))
		}
	}
	if len(tiles) != 3 {
		t.Errorf("tiles = %v, want the two showable plugins plus search", tileIDs(tiles))
	}
}

// The dashboard fills itself with anything Read that needs no input, on the
// reasoning that such a capability is free to run and therefore free to run
// every few seconds. That reasoning is about this machine. audit.deps is
// Read, mutates nothing, and defaults its path to "." — and running it sends
// the project's dependency list to osv.dev, which is not something opening a
// TUI should do, and D9 says network calls happen on explicit user action.
//
// It very nearly shipped as the audit plugin's tile.
func TestNoPreviewKeepsACapabilityOffTheAutomaticDashboard(t *testing.T) {
	reg := registry.New()
	shows := func(id string, noPreview bool) plugin.Capability {
		return plugin.Capability{
			ID: id, Summary: id, Safety: plugin.Read, NoPreview: noPreview,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return view.Text{Body: "ran " + id}, nil
			},
		}
	}
	err := reg.Register(plugin.Plugin{
		Name: "costly", Summary: "reaches off the box", Capabilities: []plugin.Capability{
			shows("costly.phones-home", true),
			shows("costly.local", false),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := tileIDs(buildTiles(reg, config.Dashboard{}))
	for _, id := range ids {
		if id == "costly.phones-home" {
			t.Errorf("a NoPreview capability was auto-tiled: %v", ids)
		}
	}
	if !slices.Contains(ids, "costly.local") {
		t.Errorf("the plugin lost its tile entirely instead of falling through: %v", ids)
	}

	// A plugin whose only zero-config capability declines to be previewed
	// gets no tile, rather than one that runs it anyway.
	solo := registry.New()
	if err := solo.Register(plugin.Plugin{
		Name: "solo", Summary: "one costly thing",
		Capabilities: []plugin.Capability{shows("solo.phones-home", true)},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range tileIDs(buildTiles(solo, config.Dashboard{})) {
		if strings.HasPrefix(id, "solo.") {
			t.Errorf("a plugin with only a NoPreview capability got a tile: %v", id)
		}
	}
}

// The rule, applied to the real catalogue: nothing that reaches off this
// machine may run because somebody opened the dashboard.
func TestNoBuiltinTileReachesOffTheMachineUnasked(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for _, ti := range buildTiles(reg, config.Dashboard{}) {
		if ti.search {
			continue
		}
		if ti.cap.NoPreview {
			t.Errorf("%s declares NoPreview and was tiled anyway", ti.cap.ID)
		}
	}
}

// The convention: a namespace's own overview is what the dashboard shows,
// whatever order the capabilities happen to be declared in.
//
// This is the rule that makes a third-party plugin's tile a choice instead of
// an accident. Before it, the tile was the first previewable capability in
// the list, and the only way to say otherwise was to be a built-in named in
// preferredTile — so a plugin whose debug dump sorted first showed the debug
// dump on the landing screen and had no way to say otherwise.
func TestAPluginsOverviewTakesTheTileOverDeclarationOrder(t *testing.T) {
	reg := registry.New()
	err := reg.RegisterFrom(plugin.Plugin{
		Name: "acme", Summary: "a third-party plugin", Capabilities: []plugin.Capability{
			{
				ID: "acme.debug-dump", Summary: "declared first, and nobody's idea of a glance",
				Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "goroutine 1 [running]"}, nil
				},
			},
			{
				ID: "acme.overview", Summary: "what acme looks like at a glance", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "all good"}, nil
				},
			},
		},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "sha256:acme"})
	if err != nil {
		t.Fatal(err)
	}
	ids := tileIDs(buildTiles(reg, config.Dashboard{}))
	if len(ids) != 1 || ids[0] != "acme.overview" {
		t.Fatalf("tiles = %v, want exactly [acme.overview]", ids)
	}
}

// The convention does not override the capability's own refusal. NoPreview
// means running it unprompted has a cost the dashboard has no business
// paying, and a naming convention is not consent — otherwise calling an
// expensive scan `overview` would put it on a five-second timer.
func TestAnOverviewThatDeclinesPreviewDoesNotTakeTheTile(t *testing.T) {
	reg := registry.New()
	err := reg.RegisterFrom(plugin.Plugin{
		Name: "acme", Summary: "a third-party plugin", Capabilities: []plugin.Capability{
			{
				ID: "acme.overview", Summary: "an expensive glance", Safety: plugin.Read,
				NoPreview: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "scanned the world"}, nil
				},
			},
			{
				ID: "acme.list", Summary: "cheap and previewable", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "a, b, c"}, nil
				},
			},
		},
	}, registry.Origin{Path: "/usr/local/bin/rta-plugin-acme", Digest: "sha256:acme"})
	if err != nil {
		t.Fatal(err)
	}
	ids := tileIDs(buildTiles(reg, config.Dashboard{}))
	if len(ids) != 1 || ids[0] != "acme.list" {
		t.Fatalf("tiles = %v, want exactly [acme.list] — an overview that declines preview is still declining", ids)
	}
}

// An explicit pin outranks the convention, which is the only reason the map
// still exists: kv.status is the kv tile precisely because the capability
// that *would* be called an overview is the one that cannot run unattended.
func TestAnExplicitPreferredTileBeatsTheOverviewConvention(t *testing.T) {
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "kv", Summary: "a secret store", Capabilities: []plugin.Capability{
			{
				ID: "kv.overview", Summary: "declared first, and would win on the convention alone",
				Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "every secret you have"}, nil
				},
			},
			{
				ID: "kv.status", Summary: "where the store is and what can open it", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "locked"}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ids := tileIDs(buildTiles(reg, config.Dashboard{}))
	if len(ids) != 1 || ids[0] != "kv.status" {
		t.Fatalf("tiles = %v, want exactly [kv.status] — the pin must outrank the convention", ids)
	}
}

// The shipped dashboard, pinned as a whole.
//
// This replaces the preferredTile entries that used to name todo.list,
// note.list, net.info, fs.tree, sys.overview and gen.overview: five of the
// seven were restating what the rules already resolve to, which made the map
// look like the mechanism when it was only ever the exception list. Asserting
// the outcome instead covers every plugin by the same rules a third-party one
// gets, and catches the two things the map never could — a plugin that
// silently stopped having a tile, and a new one that silently gained one.
//
// gen.overview is the entry worth reading twice: it puts fresh, real, usable
// secrets on a five-second timer, which was raised explicitly and accepted
// (the reasoning is on the capability, in builtin/gen). That it now arrives
// by convention rather than by being named in a map is not that decision
// loosening — this test is where it is pinned, and gen.password taking the
// tile back fails it by name.
func TestTheShippedDashboardIsTheOneWeThinkItIs(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"todo.list",
		"note.list",
		"sys.overview",
		"net.info",
		"kv.status",
		"grant.list",
		// Unranked by pluginOrder, so alphabetical after the ranked six.
		"fs.tree",
		"gen.overview",
		"git.overview",
	}
	got := tileIDs(buildTiles(reg, config.Dashboard{}))
	if !slices.Equal(got, want) {
		t.Errorf("dashboard tiles:\n got %v\nwant %v", got, want)
	}
}

// preferredTile is a table of string IDs, so a capability rename or a
// plugin move breaks it silently: the named tile stops existing, the
// automatic picker quietly falls back to whatever is first, and the
// dashboard shows something nobody chose. Checking it against the real
// registry is what makes that a failing test instead of a surprise.
func TestEveryPreferredTileNamesACapabilityThatExists(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]plugin.Plugin{}
	for _, p := range reg.Plugins() {
		byName[p.Name] = p
	}
	for name, id := range preferredTile {
		p, ok := byName[name]
		if !ok {
			t.Errorf("preferredTile names plugin %q, which is not registered", name)
			continue
		}
		found := false
		for _, c := range p.Capabilities {
			if c.ID == id {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("preferredTile[%q] = %q, which %q does not have", name, id, name)
		}
	}
}

// A pin that is not previewable does not fail loudly — pluginTile skips it
// and falls through to the convention — so the dashboard would quietly show
// something nobody chose while the map still claimed otherwise. That is the
// exact failure the map exists to prevent, so it is worth a test of its own:
// a pin must be a pin.
//
// The three reasons a capability is not previewable are all fatal here. A
// tile refreshes on a timer with nobody watching, so it must not mutate
// (Read), must be answerable from its defaults alone — one that needs an
// argument renders the same "missing input" error forever — and must not have
// declared NoPreview, which the pin has no standing to overrule.
func TestEveryPreferredTileIsActuallyUsableAsATile(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range preferredTile {
		c, ok := reg.Capability(id)
		if !ok {
			continue // reported by the test above
		}
		if previewable(c) {
			continue
		}
		switch {
		case c.Safety != plugin.Read:
			t.Errorf("%s backs the %s tile but is %s — a tile must not mutate on a timer", id, name, c.Safety)
		case formNeeded(c):
			t.Errorf("%s backs the %s tile but needs an input with no default", id, name)
		default:
			t.Errorf("%s backs the %s tile but declares NoPreview — the pin is silently ignored", id, name)
		}
	}
}

// heightRegistry has one plugin whose content is a single short line and one
// whose content overflows any reasonable tile height, for exercising
// responsive row heights.
func heightRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	short := plugin.Capability{
		ID: "short.info", Summary: "short", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "one line"}, nil
		},
	}
	long := plugin.Capability{
		ID: "tall.info", Summary: "tall", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: strings.Repeat("line\n", 30)}, nil
		},
	}
	for _, p := range []plugin.Plugin{
		{Name: "short", Summary: "short", Capabilities: []plugin.Capability{short}},
		{Name: "tall", Summary: "tall", Capabilities: []plugin.Capability{long}},
	} {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// A row whose only tile has one line of content shrinks well below the old
// fixed tileHeight — the whole point of the change.
func TestAShortTileShrinksItsRow(t *testing.T) {
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "short.info"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	sm := sized.(Model)
	sm.tiles[1].view = view.Text{Body: "one line"}
	heights := sm.rowHeights()
	if len(heights) != 1 || heights[0] != tileMinHeight {
		t.Fatalf("heights = %v, want a single row at the %d floor", heights, tileMinHeight)
	}
}

// A tile whose content genuinely overflows still gets the full tileHeight —
// nothing that used to fit (or truncate the same way) regresses.
func TestATallTileKeepsTheOldMaxHeight(t *testing.T) {
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "tall.info"}}}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	sm := sized.(Model)
	// rowHeights reads each tile's already-fetched content; a bare
	// WindowSizeMsg never runs the async refresh command, so simulate one
	// having completed rather than asserting on the "loading…" placeholder.
	sm.tiles[1].view = view.Text{Body: strings.Repeat("line\n", 30)}
	heights := sm.rowHeights()
	if len(heights) != 1 || heights[0] != tileHeight {
		t.Fatalf("heights = %v, want a single row at the max %d", heights, tileHeight)
	}
}

// Two tiles sharing a row must render at the same height even when their
// natural content length differs wildly — the taller one sets the row.
func TestTilesSharingARowMatchTheTallestOne(t *testing.T) {
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "short.info"}, {ID: "tall.info"}}}, nil)
	// Wide enough for a 2-column grid (>= 80).
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.tiles[1].view = view.Text{Body: "one line"}
	sm.tiles[2].view = view.Text{Body: strings.Repeat("line\n", 30)}
	heights := sm.rowHeights()
	if len(heights) != 1 || heights[0] != tileHeight {
		t.Fatalf("heights = %v, want one row at the max %d, set by the tall tile", heights, tileHeight)
	}
	g := sm.grid()
	shortPanel := renderTile(sm.tiles[1], g.tileW, heights[0], false)
	tallPanel := renderTile(sm.tiles[2], g.tileW, heights[0], false)
	shortLines, tallLines := strings.Count(shortPanel, "\n"), strings.Count(tallPanel, "\n")
	if shortLines != tallLines {
		t.Fatalf("row neighbors have different heights: short=%d tall=%d lines", shortLines, tallLines)
	}
}

// Mouse hit-testing has to walk cumulative row heights now instead of
// dividing by one constant — the riskiest part of making rows responsive,
// and otherwise uncovered: nothing else in this package drives tileAt.
func TestTileAtWalksVariableRowHeights(t *testing.T) {
	m := New(heightRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "short.info"}, {ID: "tall.info"}}}, nil)
	// 60 cols -> 1 column grid, so short.info and tall.info land in separate
	// rows (heights 6 and 11) rather than sharing one.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	sm := sized.(Model)
	sm.tiles[1].view = view.Text{Body: "one line"}
	sm.tiles[2].view = view.Text{Body: strings.Repeat("line\n", 30)}
	heights := sm.rowHeights()
	if len(heights) != 2 || heights[0] != tileMinHeight || heights[1] != tileHeight {
		t.Fatalf("heights = %v, want [%d %d]", heights, tileMinHeight, tileHeight)
	}

	// Row 0 (short.info, height 6) starts right after the search band.
	row0Start := 1 + searchTileHeight
	row1Start := row0Start + tileMinHeight
	for _, y := range []int{row0Start, row0Start + tileMinHeight - 1} {
		if got := sm.tileAt(5, y); got != 1 {
			t.Errorf("tileAt(5, %d) = %d, want 1 (short.info)", y, got)
		}
	}
	for _, y := range []int{row1Start, row1Start + tileHeight - 1} {
		if got := sm.tileAt(5, y); got != 2 {
			t.Errorf("tileAt(5, %d) = %d, want 2 (tall.info)", y, got)
		}
	}
	if got := sm.tileAt(5, row1Start+tileHeight); got != -1 {
		t.Errorf("tileAt below the last row = %d, want -1", got)
	}
}

// …but it stays reachable: off the dashboard must not mean out of the app.
func TestPluginWithNoTileIsStillSearchable(t *testing.T) {
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	m.query = "zeta"
	if got := m.searchResults(); len(got) != 2 {
		t.Errorf("search found %d zeta capabilities, want the plugin's two", len(got))
	}
}

func TestArrangeHidesAndOrders(t *testing.T) {
	reg := multiRegistry(t)

	hidden := buildTiles(reg, config.Dashboard{Hidden: []string{"beta.info"}})
	for _, id := range tileIDs(hidden) {
		if id == "beta.info" {
			t.Errorf("hidden tile still present: %v", tileIDs(hidden))
		}
	}

	ordered := buildTiles(reg, config.Dashboard{Order: []string{"beta.info"}})
	got := tileIDs(ordered)
	if len(got) < 2 || got[0] != "beta.info" {
		t.Errorf("order = %v, want the named ids first", got)
	}
	// An unnamed plugin keeps its place after the ordered ones rather than
	// disappearing — which is what lets a new plugin still show up.
	if got[len(got)-1] != "alpha.info" {
		t.Errorf("unordered tile lost: %v", got)
	}
}

// An id that no longer resolves must not break the arrangement.
func TestArrangeIgnoresUnknownIDs(t *testing.T) {
	tiles := buildTiles(multiRegistry(t), config.Dashboard{
		Hidden: []string{"gone.away"}, Order: []string{"also.gone"},
	})
	if len(tiles) != 3 {
		t.Errorf("tiles = %v, want both showable plugins plus search", tileIDs(tiles))
	}
}

// --- Editing the arrangement from inside the dashboard -----------------------

// dashboardModel returns a sized model with its config pointed at a temp file.
func dashboardModel(t *testing.T) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	return sized.(Model)
}

func savedConfig(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.LoadFile()
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestHidePersistsAndTakesEffectImmediately(t *testing.T) {
	m := dashboardModel(t)
	m.selected = 1
	target := m.tiles[1].cap.ID

	after, _ := m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	am := after.(Model)
	for _, id := range tileIDs(am.tiles) {
		if id == target {
			t.Fatalf("hidden tile still on screen: %v", tileIDs(am.tiles))
		}
	}
	if am.flash == "" || !strings.Contains(am.flash, target) {
		t.Errorf("flash = %q, should name what was hidden", am.flash)
	}
	// …and the config says so, so the next run opens on what was left.
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Hidden) != 1 || cfg.Dashboard.Hidden[0] != target {
		t.Errorf("saved hidden = %v, want [%s]", cfg.Dashboard.Hidden, target)
	}
	// The saved arrangement reproduces what is on screen.
	if got := tileIDs(buildTiles(multiRegistry(t), cfg.Dashboard)); len(got) != len(tileIDs(am.tiles)) {
		t.Errorf("reloaded = %v, on screen = %v", got, tileIDs(am.tiles))
	}
}

func TestMovePersistsTheWholeOrder(t *testing.T) {
	m := dashboardModel(t)
	before := tileIDs(m.tiles)
	m.selected = 1

	after, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	am := after.(Model)
	got := tileIDs(am.tiles)
	if got[0] != before[1] || got[1] != before[0] {
		t.Fatalf("move right: %v -> %v", before, got)
	}
	if am.selected != 2 {
		t.Errorf("selection should follow the tile it moved, got %d", am.selected)
	}
	cfg := savedConfig(t)
	if len(cfg.Dashboard.Order) != len(got) {
		t.Fatalf("saved order = %v, want the whole visible order", cfg.Dashboard.Order)
	}
	for i, id := range got {
		if cfg.Dashboard.Order[i] != id {
			t.Fatalf("saved order = %v, on screen = %v", cfg.Dashboard.Order, got)
		}
	}
}

// Moving off either end is a no-op, not a crash or a wrap.
func TestMoveStopsAtTheEnds(t *testing.T) {
	m := dashboardModel(t)
	before := tileIDs(m.tiles)

	m.selected = 1
	first, _ := m.Update(tea.KeyPressMsg{Code: '[', Text: "["})
	if got := tileIDs(first.(Model).tiles); got[0] != before[0] {
		t.Errorf("moving the first tile left changed the order: %v", got)
	}
	m.selected = len(m.tiles) - 1
	last, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if got := tileIDs(last.(Model).tiles); got[len(got)-1] != before[len(before)-1] {
		t.Errorf("moving the last tile right changed the order: %v", got)
	}
}

// The search tile is the front door and cannot be hidden or shuffled away.
func TestSearchTileIsNotArrangeable(t *testing.T) {
	m := dashboardModel(t)
	m.selected = 0

	hidden, _ := m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	if !hidden.(Model).tiles[0].search {
		t.Error("the search tile was hidden")
	}
	moved, _ := m.Update(tea.KeyPressMsg{Code: ']', Text: "]"})
	if !moved.(Model).tiles[0].search {
		t.Error("the search tile was moved out of first place")
	}
}

// Saving the dashboard must not fold this shell's environment into the file:
// one session's RTA_OUTPUT is not a persistent preference.
func TestSavingDoesNotBakeInEnvironmentOverrides(t *testing.T) {
	m := dashboardModel(t)
	t.Setenv("RTA_OUTPUT", "json")
	m.selected = 1

	after, _ := m.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	if flash := after.(Model).flash; strings.Contains(flash, "session only") {
		t.Fatalf("save failed: %s", flash)
	}
	if got := savedConfig(t).Output; got != "" {
		t.Errorf("saved output = %q — an env override leaked into the file", got)
	}
}

// An unwritable config must not take the edit down with it: the arrangement
// still applies for this session, and the footer says it did not stick.
func TestUnwritableConfigDegradesToThisSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "readonly", "config.yaml"))
	if err := os.WriteFile(filepath.Join(dir, "readonly"), nil, 0o400); err != nil {
		t.Fatal(err)
	}
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.selected = 1
	target := sm.tiles[1].cap.ID

	after, _ := sm.Update(tea.KeyPressMsg{Code: 'H', Text: "H"})
	am := after.(Model)
	for _, id := range tileIDs(am.tiles) {
		if id == target {
			t.Error("the hide did not apply for this session")
		}
	}
	if !strings.Contains(am.flash, "session only") {
		t.Errorf("flash = %q, should say the change did not persist", am.flash)
	}
}

// searchRegistry has more capabilities than the bar has lines.
func searchRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	caps := make([]plugin.Capability, 0, 8)
	for _, name := range []string{"one", "two", "three", "four", "five", "six", "seven", "eight"} {
		caps = append(caps, plugin.Capability{
			ID: "many." + name, Summary: name, Safety: plugin.Read, Idempotent: true,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil },
		})
	}
	if err := reg.Register(plugin.Plugin{Name: "many", Summary: "lots", Capabilities: caps}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// The bar shows three lines. It must not therefore find three capabilities:
// a query that matches eight and silently reports three is a lie you cannot
// see, and the capability you wanted looks like it does not exist.
func TestSearchKeepsEveryMatch(t *testing.T) {
	m := New(searchRegistry(t), config.Dashboard{}, nil)
	m.query = "many"
	if got := len(m.searchResults()); got != 8 {
		t.Fatalf("searchResults = %d, want every match", got)
	}
	window, first := m.searchWindow(m.searchResults())
	if len(window) != searchMatches || first != 0 {
		t.Errorf("window = %d lines at %d, want %d at 0", len(window), first, searchMatches)
	}
}

// Moving down past the third match scrolls the window instead of stopping.
func TestSearchWindowFollowsTheSelection(t *testing.T) {
	m := New(searchRegistry(t), config.Dashboard{}, nil)
	m.query = "many"
	results := m.searchResults()

	for _, tc := range []struct{ sel, first int }{
		{0, 0}, {2, 0}, {3, 1}, {7, 5},
		// Selections past the end clamp rather than slicing out of range.
		{99, 5},
	} {
		m.searchSel = tc.sel
		window, first := m.searchWindow(results)
		if first != tc.first || len(window) != searchMatches {
			t.Errorf("sel %d: window of %d at %d, want %d at %d",
				tc.sel, len(window), first, searchMatches, tc.first)
		}
	}
}

// A short result list must not be padded or scrolled.
func TestSearchWindowLeavesShortListsAlone(t *testing.T) {
	m := New(searchRegistry(t), config.Dashboard{}, nil)
	m.query = "many.one"
	window, first := m.searchWindow(m.searchResults())
	if len(window) != 1 || first != 0 {
		t.Errorf("window = %d at %d, want the single match at 0", len(window), first)
	}
}

// Keys must be able to reach every match, not only the visible three.
func TestSearchKeysReachTheWholeList(t *testing.T) {
	m := New(searchRegistry(t), config.Dashboard{}, nil)
	m.query = "many"
	m.searchEditing = true

	var model tea.Model = m
	for range 7 {
		model, _ = model.(Model).updateSearch(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	if got := model.(Model).searchSel; got != 7 {
		t.Fatalf("after 7 downs searchSel = %d, want the eighth match", got)
	}
	// …and enter opens the one under the caret, not the one that happens to
	// be third.
	sm := model.(Model)
	want := sm.searchResults()[7].ID
	opened, _ := sm.updateSearch(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got := opened.(Model).current.ID; got != want {
		t.Errorf("enter opened %s, want the selected match %s", got, want)
	}
}

// --- The plugins pane -------------------------------------------------------

// Hiding was one-way: `H` took a tile off and the only way back was the
// config file. A hide you cannot undo is not a preference.
func TestPluginPaneShowsAndHidesTiles(t *testing.T) {
	m := dashboardModel(t)
	m.plugins = pluginRows(m.reg, m.dash)

	idx := -1
	for i, row := range m.plugins {
		if row.plugin.Name == "beta" {
			idx = i
		}
	}
	if idx < 0 || !m.plugins[idx].canTile() {
		t.Fatalf("no tileable plugin to test with: %+v", m.plugins)
	}

	if note := m.toggleShown(idx); note == "" {
		t.Fatal("toggling said nothing")
	}
	if m.plugins[idx].shown {
		t.Error("plugin still marked shown after hiding")
	}
	for _, id := range tileIDs(m.tiles) {
		if id == "beta.info" {
			t.Errorf("hidden tile still on the dashboard: %v", tileIDs(m.tiles))
		}
	}
	if cfg := savedConfig(t); len(cfg.Dashboard.Hidden) != 1 {
		t.Errorf("saved hidden = %v", cfg.Dashboard.Hidden)
	}

	// …and back again, which is the whole point.
	m.toggleShown(idx)
	if !m.plugins[idx].shown {
		t.Error("plugin not marked shown after showing")
	}
	found := false
	for _, id := range tileIDs(m.tiles) {
		if id == "beta.info" {
			found = true
		}
	}
	if !found {
		t.Errorf("shown tile did not come back: %v", tileIDs(m.tiles))
	}
	if cfg := savedConfig(t); len(cfg.Dashboard.Hidden) != 0 {
		t.Errorf("saved hidden = %v, want empty", cfg.Dashboard.Hidden)
	}
}

// A plugin with nothing glanceable is listed — that is how you learn it is
// installed — and says why it has no tile instead of silently ignoring the key.
func TestPluginPaneListsPluginsWithoutTiles(t *testing.T) {
	m := dashboardModel(t)
	m.plugins = pluginRows(m.reg, m.dash)
	if len(m.plugins) != len(m.reg.Plugins()) {
		t.Fatalf("pane lists %d of %d plugins", len(m.plugins), len(m.reg.Plugins()))
	}
	idx := -1
	for i, row := range m.plugins {
		if row.plugin.Name == "zeta" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("the plugin with no tile is missing from the inventory")
	}
	if m.plugins[idx].canTile() {
		t.Error("zeta should have no tile")
	}
	if note := m.toggleShown(idx); !strings.Contains(note, "nothing to show") {
		t.Errorf("toggling an untileable plugin said %q", note)
	}
	if !strings.Contains(pluginDetail(m.plugins[idx]), "no dashboard tile") {
		t.Errorf("detail = %q", pluginDetail(m.plugins[idx]))
	}
}

// `p` opens the pane and `esc` closes it, restarting the tile refresh.
func TestPluginPaneOpensAndCloses(t *testing.T) {
	m := dashboardModel(t)
	opened, _ := m.Update(tea.KeyPressMsg{Code: 'p', Text: "p"})
	om := opened.(Model)
	if om.mode != modePlugins {
		t.Fatalf("mode = %v, want the plugins pane", om.mode)
	}
	if len(om.plugins) == 0 {
		t.Error("pane opened empty")
	}
	closed, cmd := om.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if closed.(Model).mode != modeDashboard || cmd == nil {
		t.Error("esc did not return to a refreshing dashboard")
	}
}

// --- The capability catalogue ----------------------------------------------

// Capabilities belong to plugins, and the plugin is the unit people think in.
func TestCatalogueGroupsByPlugin(t *testing.T) {
	reg := multiRegistry(t)
	items := catalogueItems(reg)

	var headers []string
	current := ""
	for _, item := range items {
		switch it := item.(type) {
		case pluginHeader:
			headers = append(headers, it.p.Name)
			current = it.p.Name
		case capItem:
			if current == "" {
				t.Fatalf("%s appears before any plugin header", it.c.ID)
			}
			if !strings.HasPrefix(it.c.ID, current+".") {
				t.Errorf("%s is filed under %q", it.c.ID, current)
			}
		}
	}
	if len(headers) != len(reg.Plugins()) {
		t.Errorf("headers = %v, want one per plugin", headers)
	}
	// Every capability is still listed: grouping must not lose any.
	if got := len(items) - len(headers); got != len(reg.Capabilities()) {
		t.Errorf("catalogue holds %d capabilities, registry has %d", got, len(reg.Capabilities()))
	}
}

// A header is a label, not a destination: landing on one leaves enter doing
// nothing.
func TestCursorNeverRestsOnAHeader(t *testing.T) {
	m := New(multiRegistry(t), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	sm := sized.(Model)
	sm.mode = modeBrowse

	if _, isHeader := sm.list.SelectedItem().(pluginHeader); isHeader {
		t.Fatal("the list opens on a section header")
	}
	var model tea.Model = sm
	for i := range len(catalogueItems(sm.reg)) {
		model, _ = model.(Model).Update(tea.KeyPressMsg{Code: tea.KeyDown})
		if _, isHeader := model.(Model).list.SelectedItem().(pluginHeader); isHeader {
			t.Fatalf("cursor landed on a header after %d downs", i+1)
		}
	}
	for range len(catalogueItems(sm.reg)) {
		model, _ = model.(Model).Update(tea.KeyPressMsg{Code: tea.KeyUp})
		if _, isHeader := model.(Model).list.SelectedItem().(pluginHeader); isHeader {
			t.Fatal("cursor landed on a header going up")
		}
	}
}

// The permission column answers "what could an agent do here?" in the same
// vocabulary everywhere else uses.
func TestPermissionLabel(t *testing.T) {
	cases := []struct {
		c    plugin.Capability
		want []string
	}{
		{plugin.Capability{ID: "a.b", Safety: plugin.Read}, []string{"read"}},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write}, []string{"write"}},
		{plugin.Capability{ID: "a.b", Safety: plugin.Destructive}, []string{"destructive", "grant"}},
		{plugin.Capability{ID: "a.b", Safety: plugin.Write, NeedsGrant: true}, []string{"write", "grant"}},
	}
	for _, tc := range cases {
		got := permissionLabel(tc.c)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%s label %q missing %q", tc.c.Safety, got, want)
			}
		}
	}
	// A plain read must not claim to need one.
	if strings.Contains(permissionLabel(plugin.Capability{ID: "a.b", Safety: plugin.Read}), "grant") {
		t.Error("a read should not mention grants")
	}
}

// A tile whose content has a natural minimum width — a 44-character base64
// key, a 36-character UUID — claims the columns it needs rather than being
// shrunk into one, where the value would be wrapped or truncated and stop
// being a value you can read off the screen.
//
// The wide tile is placed in the MIDDLE of the list on purpose. The shipped
// arrangement happens to have an odd number of tiles, so the real wide one
// lands alone on the last row whether the layout understands spans or not —
// a fixture that would pass with the feature deleted.
func TestAWideTileTakesARowOfItsOwnAtFullWidth(t *testing.T) {
	m := Model{width: 120, height: 44}
	m.tiles = []tile{{search: true}}
	// MinWidth rather than a host-side list of IDs: the capability is what
	// knows its content does not shrink. 120 cells over three columns is 39
	// each, so 100 needs all three.
	for i, minW := range []int{0, 0, 100, 0, 0} {
		m.tiles = append(m.tiles, tile{
			cap:  plugin.Capability{ID: fmt.Sprintf("p%d.show", i), MinWidth: minW},
			view: view.Text{Body: "a\nb"},
		})
	}
	var got [][]string
	for _, row := range m.tileRows() {
		names := []string{}
		for _, i := range row {
			names = append(names, m.tiles[i].cap.ID)
		}
		got = append(got, names)
	}
	want := [][]string{{"p0.show", "p1.show"}, {"p2.show"}, {"p3.show", "p4.show"}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rows = %v, want %v — the wide tile must break the row it falls in", got, want)
	}
	// And it must actually be drawn at the full width, not a column of it.
	if w := m.tileWidth(3); w != m.width {
		t.Errorf("wide tile is %d cells, want the full %d", w, m.width)
	}
	// The row the wide tile broke early is short, and its tiles share out
	// what is left rather than leaving a hole beside them. Every row is
	// exactly the screen wide, so the right-hand edge does not wobble.
	for r, row := range m.tileRows() {
		total := len(row) - 1
		for _, i := range row {
			total += m.tileWidth(i)
		}
		if total != m.width {
			t.Errorf("row %d draws %d cells, want exactly %d", r, total, m.width)
		}
	}
}

// Reading down a column and back up has to return you to the column you
// started in. Rows are not all the same length — a wide tile closes the row
// before it early — and taking the column from the current position let the
// short row swallow it: from the right-hand column, four presses down and
// four back up landed on the left-hand one, silently, with nothing on screen
// to say a column had been lost.
func TestVerticalMovementKeepsItsColumn(t *testing.T) {
	m := Model{width: 120, height: 44}
	m.tiles = []tile{{search: true}}
	for i, minW := range []int{0, 0, 0, 0, 0, 0, 100, 0} {
		m.tiles = append(m.tiles, tile{
			cap:  plugin.Capability{ID: fmt.Sprintf("p%d.show", i), MinWidth: minW},
			view: view.Text{Body: "a\nb"},
		})
	}
	rows := m.tileRows()
	if len(rows) < 3 {
		t.Fatalf("need a few rows to walk: %v", rows)
	}
	// Start in the last column of the first row.
	start := rows[0][len(rows[0])-1]
	m.selected = start
	_, m.goalCol = m.rowAndCol(start)
	startCol := m.goalCol

	depth := len(rows) - 1
	for range depth {
		m.moveSelection(0, 1)
	}
	for range depth {
		m.moveSelection(0, -1)
	}
	if m.selected != start {
		_, col := m.rowAndCol(m.selected)
		t.Errorf("down %d then up %d landed on tile %d (column %d), not back on %d (column %d)",
			depth, depth, m.selected, col, start, startCol)
	}
}

// Hit-testing used to divide x by the column width, which is right only
// while every row has the same number of equally-wide tiles. On a full-width
// row, the far right of the screen is still that one tile.
func TestClickingAnywhereOnAWideRowHitsThatTile(t *testing.T) {
	m := wideRowModel(t)
	rows := m.tileRows()
	heights := m.rowHeights()
	wideRow, wideIdx := -1, -1
	for r, row := range rows {
		if len(row) == 1 && m.span(row[0]) == m.grid().cols {
			wideRow, wideIdx = r, row[0]
		}
	}
	if wideRow < 0 {
		t.Skip("no wide tile")
	}
	y := 1 + searchTileHeight
	for r := 0; r < wideRow; r++ {
		y += heights[r]
	}
	for _, x := range []int{0, m.width / 2, m.width - 1} {
		if got := m.tileAt(x, y); got != wideIdx {
			t.Errorf("tileAt(%d, %d) = %d, want the wide tile %d", x, y, got, wideIdx)
		}
	}
	if got := m.tileAt(m.width+5, y); got != -1 {
		t.Errorf("a click past the right edge hit tile %d", got)
	}
}

// Moving down onto a row that holds fewer tiles than the one above must land
// somewhere real. The old arithmetic added cols to a flat index, which on a
// short row walked straight past it.
func TestMovingOntoAShorterRowLandsOnATile(t *testing.T) {
	m := wideRowModel(t)
	rows := m.tileRows()
	if len(rows) < 2 {
		t.Skip("need at least two rows")
	}
	for r := 0; r < len(rows)-1; r++ {
		for _, from := range rows[r] {
			m.selected = from
			m.moveSelection(0, 1)
			if m.selected < 1 || m.selected >= len(m.tiles) {
				t.Fatalf("down from tile %d left selection at %d", from, m.selected)
			}
			gotRow, _ := m.rowAndCol(m.selected)
			if gotRow != r+1 {
				t.Errorf("down from row %d (tile %d) landed on row %d", r, from, gotRow)
			}
		}
	}
}

// Every tile has to be reachable by walking, or a tile that exists is a tile
// nobody can open.
func TestEveryTileIsReachableByMovingDownAndRight(t *testing.T) {
	m := wideRowModel(t)
	seen := map[int]bool{}
	m.selected = 1
	for range len(m.tiles) * 4 {
		seen[m.selected] = true
		before := m.selected
		m.moveSelection(1, 0)
		if m.selected == before {
			break
		}
	}
	for i := 1; i < len(m.tiles); i++ {
		if !seen[i] {
			t.Errorf("tile %d (%s) cannot be reached by moving right", i, m.tiles[i].cap.ID)
		}
	}
}

func wideRowModel(t *testing.T) Model {
	t.Helper()
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	um, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 44})
	m = um.(Model)
	for i := range m.tiles {
		if !m.tiles[i].search {
			m.tiles[i].view = view.Text{Body: "a\nb\nc"}
		}
	}
	return m
}

// A tile runs on load and again every five seconds, with no form and no
// confirmation: the destructive gate lives on the CLI and on the TUI's browse
// path, and the tile path goes through neither. The automatic dashboard only
// ever picks Read capabilities, but the config list took any ID at all, so
//
//	{id: kv.rm, with: {key: old-token}}
//
// deleted the key on startup and kept deleting it, forever, in silence.
func TestConfiguredTilesRefuseAnythingThatWrites(t *testing.T) {
	reg := registry.New()
	made := map[string]int{}
	cap := func(id string, s plugin.Safety) plugin.Capability {
		return plugin.Capability{
			ID: id, Summary: id, Safety: s,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				made[id]++
				return view.Text{Body: "ran"}, nil
			},
		}
	}
	err := reg.Register(plugin.Plugin{
		Name: "demo", Summary: "d", Capabilities: []plugin.Capability{
			cap("demo.look", plugin.Read),
			cap("demo.write", plugin.Write),
			cap("demo.rm", plugin.Destructive),
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	ids := tileIDs(buildTiles(reg, config.Dashboard{Tiles: []config.Tile{
		{ID: "demo.rm"}, {ID: "demo.write"}, {ID: "demo.look"},
	}}))
	for _, banned := range []string{"demo.rm", "demo.write"} {
		if slices.Contains(ids, banned) {
			t.Errorf("%s was pinned as a tile: %v", banned, ids)
		}
	}
	if !slices.Contains(ids, "demo.look") {
		t.Errorf("the read capability lost its tile: %v", ids)
	}
	if made["demo.rm"]+made["demo.write"] != 0 {
		t.Errorf("building the dashboard ran a mutating capability: %v", made)
	}

	// A dashboard whose every configured tile is refused falls back to the
	// automatic set rather than showing nothing.
	only := tileIDs(buildTiles(reg, config.Dashboard{Tiles: []config.Tile{{ID: "demo.rm"}}}))
	if !slices.Contains(only, "demo.look") {
		t.Errorf("no tiles at all after refusing the configured one: %v", only)
	}
}

// A tile is a compact preview, and opening one gives it the whole screen.
// A tile capability that is not Detailed therefore expands into precisely
// what the tile already showed — the pane is bigger and says nothing more,
// which reads as a dead end rather than as a deliberate omission.
//
// fs is why this test exists. Its tile is fs.tree, because fs.usage is a
// recursive scan carrying NoPreview; but Detailed lived on fs.usage, so fs
// was the one plugin on the dashboard whose tile opened into itself. Nobody
// noticed because every other tile capability happened to have it, and
// "happened to" is what a structural test converts into a rule.
//
// A future tile with genuinely nothing more to show is a legitimate answer.
// It just has to be written down here rather than discovered by opening it.
func TestEveryBuiltinTileHasAFullScreenView(t *testing.T) {
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	// Capabilities whose compact view is already the whole answer. Empty on
	// purpose: add an entry with the reason, do not delete the test.
	sameAtAnySize := map[string]string{}

	for _, ti := range buildTiles(reg, config.Dashboard{}) {
		if ti.search {
			continue
		}
		if why, ok := sameAtAnySize[ti.cap.ID]; ok {
			if ti.cap.Detailed {
				t.Errorf("%s is listed as having no fuller view (%s) but declares Detailed", ti.cap.ID, why)
			}
			continue
		}
		if !ti.cap.Detailed {
			t.Errorf("%s is a dashboard tile but is not Detailed, so opening it shows the tile again; "+
				"give it a detail page or record here why it has none", ti.cap.ID)
		}
	}
}

// The inventory must say where a plugin came from.
//
// plugin.Plugin carries a name, a summary and its capabilities and nothing
// about its origin, so before external plugins existed there was nothing to
// say — and once they existed, `kv` and a binary somebody dropped on $PATH
// rendered as the same kind of thing, on the one screen whose whole job is
// "what do I actually have". Every security property in pluginhost binds a
// plugin to its artifact (ADR 0015); this is where a person sees that binding
// without running doctor.
func TestThePluginPaneDistinguishesBuiltInFromExternal(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	run := func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "x"}, nil }
	mk := func(ns string) plugin.Plugin {
		return plugin.Plugin{Name: ns, Summary: ns + " summary", Capabilities: []plugin.Capability{
			{ID: ns + ".info", Summary: "info", Safety: plugin.Read, Idempotent: true, Run: run},
		}}
	}
	reg := registry.New()
	if err := reg.Register(mk("home")); err != nil {
		t.Fatal(err)
	}
	// Registered with an origin, which is the only way a plugin becomes
	// external now — there is no side map to inject.
	away := registry.Origin{Path: "/somewhere/rta-plugin-away", Digest: strings.Repeat("ab", 32)}
	if err := reg.RegisterFrom(mk("away"), away); err != nil {
		t.Fatal(err)
	}

	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, m.dash)
	m.width, m.height = 200, 60

	var ext, builtin pluginRow
	for _, row := range m.plugins {
		switch row.plugin.Name {
		case "away":
			ext = row
		case "home":
			builtin = row
		}
	}
	if !ext.external() {
		t.Fatal("the plugin registered with an origin does not report as external")
	}
	if builtin.external() {
		t.Fatal("a plugin registered without one reports as external")
	}

	extDetail, builtinDetail := pluginDetail(ext), pluginDetail(builtin)
	if !strings.Contains(extDetail, away.Path) {
		t.Errorf("the external plugin's line does not say where it came from: %s", extDetail)
	}
	if !strings.Contains(extDetail, away.Short()) {
		t.Errorf("the external plugin's line does not carry its digest: %s", extDetail)
	}
	if !strings.Contains(builtinDetail, "built in") {
		t.Errorf("a built-in is not named as one: %s", builtinDetail)
	}
	if strings.Contains(builtinDetail, "$PATH") {
		t.Errorf("a built-in claims to come from $PATH: %s", builtinDetail)
	}

	if out := m.pluginsView(); !strings.Contains(out, "built in") {
		t.Error("the rendered pane never says a plugin is built in")
	}
}

// Reach is the other half: "this is third-party code" and "this can destroy
// things" are the two facts wanted in one glance, and neither is much use
// alone. It is the worst class the plugin holds, which is the same rule
// `rta plugin list` uses for its own column.
func TestThePluginPaneShowsWhatEachPluginCanDo(t *testing.T) {
	// The real catalogue, because it is the only registry that holds all
	// three safety classes — multiRegistry is Read throughout, so asserting
	// against it would have proved the pane renders one word.
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, m.dash)
	m.width, m.height = 200, 60

	for _, row := range m.plugins {
		want := plugin.Read
		for _, c := range row.plugin.Capabilities {
			if c.Safety == plugin.Write && want == plugin.Read {
				want = plugin.Write
			}
			if c.Safety == plugin.Destructive {
				want = plugin.Destructive
			}
		}
		if got := row.reach(); got != want {
			t.Errorf("%s reach = %q, want %q", row.plugin.Name, got, want)
		}
	}

	out := m.pluginsView()
	// The catalogue holds all three classes, so all three words must appear.
	for _, want := range []string{"read", "write", "destructive"} {
		if !strings.Contains(out, want) {
			t.Errorf("the pane never renders reach %q", want)
		}
	}
}

// The selected plugin must be on screen.
//
// The pane clipped instead of scrolling, so at 80x24 — the default terminal
// size since forever — the twelfth plugin was invisible while `j` still
// selected it and `space` still toggled its tile. Acting on a row you cannot
// see reads as the app being broken rather than as the pane being short.
func TestThePluginPaneScrollsToTheSelection(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, m.dash)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 24})
	m = sized.(Model)
	m.mode = modePlugins

	if len(m.plugins) < 8 {
		t.Fatalf("only %d plugins: this test needs more than fit on one screen", len(m.plugins))
	}
	if fits := m.visiblePlugins(m.pluginBodyHeight()); fits >= len(m.plugins) {
		t.Skipf("all %d plugins fit in %d rows, nothing to scroll", len(m.plugins), fits)
	}

	// Walk down through every plugin the way a user does, and require the
	// selected one to be rendered at each step.
	for i := range m.plugins {
		m.pluginSel = i
		m.clampPluginScroll(m.pluginBodyHeight())
		name := strings.ToUpper(m.plugins[i].plugin.Name)
		if out := m.pluginsView(); !strings.Contains(out, " "+name+" ") {
			t.Fatalf("plugin %d (%s) is selected but not on screen:\n%s", i, name, out)
		}
	}
	// And back up again, since scrolling down and scrolling up are different
	// arithmetic and only one of them was ever exercised by hand.
	for i := len(m.plugins) - 1; i >= 0; i-- {
		m.pluginSel = i
		m.clampPluginScroll(m.pluginBodyHeight())
		name := strings.ToUpper(m.plugins[i].plugin.Name)
		if out := m.pluginsView(); !strings.Contains(out, " "+name+" ") {
			t.Fatalf("plugin %d (%s) is selected but not on screen scrolling up:\n%s", i, name, out)
		}
	}
}

// When there is more than fits, the pane says so rather than implying the
// list ends where the panel does.
func TestThePluginPaneSaysWhenThereIsMore(t *testing.T) {
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	reg, err := all.Registry()
	if err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{}, nil)
	m.plugins = pluginRows(reg, m.dash)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 24})
	m = sized.(Model)

	total := len(m.plugins)
	if m.visiblePlugins(m.pluginBodyHeight()) >= total {
		t.Skip("everything fits at this size")
	}
	if out := m.pluginsView(); !strings.Contains(out, fmt.Sprintf("of %d", total)) {
		t.Errorf("the pane does not say how many plugins there are in total:\n%s", out)
	}

	// Tall enough for everything: the count reads as a count, not a range.
	tall, _ := m.Update(tea.WindowSizeMsg{Width: 96, Height: 200})
	m = tall.(Model)
	if out := m.pluginsView(); !strings.Contains(out, fmt.Sprintf("%d installed", total)) {
		t.Errorf("with room for everything the pane should simply count:\n%s", out)
	}
}

// A refresh result must land on the tile it came from, whatever moved.
//
// Every tile refreshes at once and the grid can be reordered with `[`/`]` or a
// tile hidden with `H` while results are in flight. Addressed by index, a
// result arriving after a reorder painted one capability's output under
// another's title — the worst failure available to a screen whose whole job
// is to be glanced at, because it is wrong and looks right.
func TestATileResultLandsOnItsOwnTileAfterAReorder(t *testing.T) {
	m := dashboardModel(t)
	if len(m.tiles) < 3 {
		t.Fatalf("only %d tiles", len(m.tiles))
	}
	// Find two capability tiles (skip the static search tile at 0).
	var a, b int = -1, -1
	for i, tl := range m.tiles {
		if tl.cap.ID == "" {
			continue
		}
		if a < 0 {
			a = i
		} else if b < 0 {
			b = i
			break
		}
	}
	if a < 0 || b < 0 {
		t.Fatal("need two capability tiles")
	}
	idA, idB := m.tiles[a].cap.ID, m.tiles[b].cap.ID

	// A refresh for A starts while A is at index a, then the grid is
	// rearranged so A and B swap places.
	inFlight := tileMsg{id: idA, idx: a, v: view.Text{Body: "RESULT-FOR-A"}}
	m.tiles[a], m.tiles[b] = m.tiles[b], m.tiles[a]

	updated, _ := m.Update(inFlight)
	m = updated.(Model)

	for _, tl := range m.tiles {
		body, _ := tl.view.(view.Text)
		switch tl.cap.ID {
		case idA:
			if body.Body != "RESULT-FOR-A" {
				t.Errorf("%s did not receive its own result, got %q", idA, body.Body)
			}
		case idB:
			if body.Body == "RESULT-FOR-A" {
				t.Errorf("%s was painted with %s's result", idB, idA)
			}
		}
	}
}

// A result for a tile that has since been hidden is dropped, not misapplied.
func TestATileResultForARemovedTileIsDropped(t *testing.T) {
	m := dashboardModel(t)
	var id string
	for _, tl := range m.tiles {
		if tl.cap.ID != "" {
			id = tl.cap.ID
			break
		}
	}
	if id == "" {
		t.Fatal("no capability tile")
	}
	// Drop every tile carrying that capability, as `H` does.
	kept := m.tiles[:0]
	for _, tl := range m.tiles {
		if tl.cap.ID != id {
			kept = append(kept, tl)
		}
	}
	m.tiles = kept
	before := len(m.tiles)

	updated, _ := m.Update(tileMsg{id: id, idx: 0, v: view.Text{Body: "STALE"}})
	m = updated.(Model)

	if len(m.tiles) != before {
		t.Errorf("tiles = %d, want %d", len(m.tiles), before)
	}
	for _, tl := range m.tiles {
		if body, _ := tl.view.(view.Text); body.Body == "STALE" {
			t.Errorf("a result for a hidden tile was painted onto %s", tl.cap.ID)
		}
	}
}
