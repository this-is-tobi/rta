package tui

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Folding a section, selecting its header, clicking it and moving it: what the
// keys and the mouse do on a dashboard that has some.

// listed is a stated dashboard of the section registry's tiles, each in the
// section it is listed with, "" for none.
func listed(pairs ...string) config.Dashboard {
	var tiles []config.Tile
	for i := 0; i < len(pairs); i += 2 {
		tiles = append(tiles, config.Tile{ID: pairs[i] + ".status", Section: pairs[i+1]})
	}
	return config.Dashboard{Tiles: tiles}
}

// savedOrder is the capabilities of the tiles the config file lists, in order.
func savedOrder(t *testing.T) string {
	t.Helper()
	cfg := savedConfig(t)
	var ids []string
	for _, tile := range append(cfg.Dashboard.Tiles, cfg.Dashboard.Add...) {
		ids = append(ids, strings.TrimSuffix(tile.ID, ".status"))
	}
	return strings.Join(ids, " ")
}

func selectHeader(m Model, name string) Model {
	m.selectStop(stop{header: name})
	m.clampScroll()
	return m
}

// Enter and space fold a header, and the selection stays on it: the header is
// what was pressed, and a folded section has nothing else to be selected.
func TestEnterAndSpaceOnAHeaderFoldAndUnfoldIt(t *testing.T) {
	for _, key := range []string{"enter", " "} {
		t.Run("key "+key, func(t *testing.T) {
			m := sectioned(t, grouped(), 120, 60)
			m = selectHeader(m, "databases")
			if got := shape(m); got != "#databases [1 2] [3] #edge [4] [5]" {
				t.Fatalf("rows = %s", got)
			}
			m = press(t, m, key)
			if !m.collapsed("databases") || shape(m) != "#databases [3] #edge [4] [5]" {
				t.Errorf("after %q: collapsed %v, rows %s", key, m.collapsed("databases"), shape(m))
			}
			if !m.onHeader("databases") {
				t.Errorf("the selection left the header: selected %d, header %q", m.selected, m.header)
			}
			if m.mode != modeDashboard {
				t.Error("enter on a header opened something")
			}
			m = press(t, m, key)
			if m.collapsed("databases") || shape(m) != "#databases [1 2] [3] #edge [4] [5]" {
				t.Errorf("after %q again: collapsed %v, rows %s", key, m.collapsed("databases"), shape(m))
			}
		})
	}
}

// z folds the section of whatever is selected, and the selection goes to the
// header it is now under.
func TestZFoldsTheSectionOfTheSelectedTileAndOfTheSelectedHeader(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	m.selected = 2 // beta, in databases
	m = press(t, m, "z")
	if !m.collapsed("databases") || m.collapsed("edge") {
		t.Fatalf("databases folded %v, edge folded %v", m.collapsed("databases"), m.collapsed("edge"))
	}
	if !m.onHeader("databases") {
		t.Errorf("the tile was folded away and the selection with it: selected %d, header %q", m.selected, m.header)
	}
	m = press(t, m, "z")
	if m.collapsed("databases") || !m.onHeader("databases") {
		t.Errorf("z on the header: folded %v, selected %d", m.collapsed("databases"), m.selected)
	}
	// A tile in no section is told so, in words that say what to press.
	m.selected = 3 // delta
	m = press(t, m, "z")
	if !strings.Contains(m.flash, "in no section") || m.flash != m.flashGuide {
		t.Errorf("z on a tile in no section said %q (guide %q)", m.flash, m.flashGuide)
	}
}

// Z is one key that undoes itself: it folds every section, and when none is open
// it unfolds them all.
func TestZFoldsEverySectionAndThenUnfoldsThem(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	m.selected = 4 // gamma, in edge
	m = press(t, m, "Z")
	if !m.collapsed("databases") || !m.collapsed("edge") || m.flash != "folded 2 sections" {
		t.Errorf("after Z: databases %v, edge %v, flash %q", m.collapsed("databases"), m.collapsed("edge"), m.flash)
	}
	if !m.onHeader("edge") {
		t.Errorf("the selected tile was folded away: selected %d, header %q", m.selected, m.header)
	}
	// One section opened by hand: Z folds the rest first, since not all are folded.
	m = press(t, m, "z")
	if m.collapsed("edge") {
		t.Fatal("z did not unfold edge")
	}
	m = press(t, m, "Z")
	if !m.collapsed("edge") {
		t.Error("Z with one section open did not fold it")
	}
	m = press(t, m, "Z")
	if m.collapsed("databases") || m.collapsed("edge") || m.flash != "unfolded 2 sections" {
		t.Errorf("Z with every section folded: databases %v, edge %v, flash %q",
			m.collapsed("databases"), m.collapsed("edge"), m.flash)
	}
}

// A dashboard that has no section has no fold key: z and Z do nothing, are not
// on the bar, and are still the keys a tile's own action may use.
func TestADashboardWithoutSectionsHasNoFoldKeys(t *testing.T) {
	m := sectioned(t, listed("alpha", "", "beta", ""), 120, 40)
	m.selected = 1
	for _, it := range m.dashFooterItems() {
		if it.display == "z" || it.display == "Z" {
			t.Errorf("the bar offers %q on a dashboard with no section", it.display)
		}
	}
	for _, key := range []string{"z", "Z"} {
		after := press(t, m, key)
		if fingerprint(after) != fingerprint(m) {
			t.Errorf("%s did something on a dashboard with no section: %q", key, after.flash)
		}
	}
	with := sectioned(t, grouped(), 120, 40)
	with.selected = 1
	shown := map[string]bool{}
	for _, it := range with.dashFooterItems() {
		shown[it.display] = true
	}
	if !shown["z"] || !shown["Z"] {
		t.Errorf("a dashboard with sections does not offer z and Z: %v", shown)
	}
}

// A click on a header folds it, anywhere on its line; a click on a tile is still
// what opens the tile.
func TestClickingAHeaderFoldsItAndClickingATileStillOpensIt(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	y := 1 + m.searchHeight() // the first row is the databases header
	for _, x := range []int{0, 40, 119} {
		next, _ := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		got := next.(Model)
		if !got.collapsed("databases") || !got.onHeader("databases") || got.mode != modeDashboard {
			t.Errorf("a click at x=%d: folded %v, on header %v, mode %v", x, got.collapsed("databases"), got.onHeader("databases"), got.mode)
		}
		again, _ := got.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		if again.(Model).collapsed("databases") {
			t.Errorf("a second click at x=%d did not unfold it", x)
		}
	}
	// The row under the header is tiles.
	next, cmd := m.Update(tea.MouseClickMsg{X: 5, Y: y + 1, Button: tea.MouseLeft})
	if got := next.(Model); got.collapsed("databases") || got.selected != 1 || cmd == nil {
		t.Errorf("a click on a tile: folded %v, selected %d, ran something %v", got.collapsed("databases"), got.selected, cmd != nil)
	}
	// A click on the search bar's line is not a header.
	next, _ = m.Update(tea.MouseClickMsg{X: 5, Y: 1, Button: tea.MouseLeft})
	if got := next.(Model); got.collapsed("databases") || got.selected != 0 {
		t.Errorf("a click on the search bar: folded %v, selected %d", got.collapsed("databases"), got.selected)
	}
}

// H takes a tile down, and a section is several tiles that come from places
// that are taken down in different ways: it says what to press and writes
// nothing.
func TestHideOnAHeaderRefusesAndWritesNothing(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	path := config.Path()
	m = selectHeader(m, "databases")
	m = press(t, m, "H")
	if m.flash == "" || m.flash != m.flashBad || !strings.Contains(m.flash, "z") {
		t.Errorf("H on a header said %q (refusal %q)", m.flash, m.flashBad)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("H on a header wrote the config")
	}
	if got := shape(m); got != "#databases [1 2] [3] #edge [4] [5]" {
		t.Errorf("rows = %s: a tile went", got)
	}
	// And the bar does not offer a key that refuses.
	for _, it := range m.dashFooterItems() {
		if it.display == "H" {
			t.Errorf("the bar offers H over a header: %q", it.label)
		}
	}
}

// A section's name is a heading on one line, and a file can state one that is not:
// a line break in it made the header two rows where the layout counts one, and cut
// a flash that names it in two. Wherever the TUI says a section it says it the way
// a listed name is said, quoted when it does not read as itself.
func TestASectionNameWithALineBreakIsOneLineWhereverItIsSaid(t *testing.T) {
	const name = "two\nlines"
	m := sectioned(t, listed("alpha", name, "beta", name, "delta", ""), 120, 60)

	header := m.renderSectionHeader(m.sectionRuns()[0], false)
	if lipgloss.Height(header) != 1 || !strings.Contains(plain(header), `"two\nlines"`) {
		t.Errorf("the header is %d lines: %q", lipgloss.Height(header), plain(header))
	}
	if got, want := lipgloss.Height(plain(m.View().Content)), m.height; got > want {
		t.Errorf("the frame is %d lines on a %d-line terminal", got, want)
	}

	h := selectHeader(m, name)
	h = press(t, h, "H")
	if h.flash == "" || strings.Contains(h.flash, "\n") || !strings.Contains(h.flash, `"two\nlines"`) {
		t.Errorf("H on the header said %q", h.flash)
	}

	m.selected = 1
	m = press(t, m, "[")
	if m.flash == "" || strings.Contains(m.flash, "\n") || !strings.Contains(m.flash, `"two\nlines"`) {
		t.Errorf("a move past the edge of the section said %q", m.flash)
	}
}

// What the bar says over a header is what the header can be asked.
func TestTheBarOverAHeaderOffersToFoldIt(t *testing.T) {
	m := selectHeader(sectioned(t, grouped(), 120, 60), "databases")
	bar := plain(fitHintBar(0, footerMaxLines, m.footerItems(modeDashboard)...))
	for _, want := range []string{"enter fold", "z fold", "Z fold all", "[ ] move"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar over a header lacks %q:\n%s", want, bar)
		}
	}
	if strings.Contains(bar, "details") {
		t.Errorf("the bar over a header offers to open it:\n%s", bar)
	}
	m = press(t, m, "enter")
	if bar = plain(fitHintBar(0, footerMaxLines, m.footerItems(modeDashboard)...)); !strings.Contains(bar, "enter unfold") {
		t.Errorf("the bar over a folded header does not offer to unfold it:\n%s", bar)
	}
}

// A fold is the session's: nothing about it reaches the file, and it survives
// the tiles being built again, which is what happens on every edit and every
// switch.
func TestAFoldIsNotWrittenAndSurvivesTheTilesBeingBuiltAgain(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	m.selected = 1
	m = press(t, m, "z")
	if _, err := os.Stat(config.Path()); err == nil {
		t.Error("folding wrote the config")
	}
	m.rebuildTiles()
	if !m.collapsed("databases") || shape(m) != "#databases [3] #edge [4] [5]" {
		t.Errorf("a rebuild undid the fold: %s", shape(m))
	}
	if !m.onHeader("databases") {
		t.Errorf("a rebuild moved the selection off the header: selected %d, header %q", m.selected, m.header)
	}
}

// An edit to how a section starts, made in another terminal, reaches the screen on
// the next tick like any other edit of the dashboard: the stamp that tells the
// session the file moved covers `sections:`.
func TestAnEditToHowASectionStartsReachesTheOpenDashboard(t *testing.T) {
	m := sectioned(t, grouped(), 120, 60)
	if m.collapsed("databases") {
		t.Fatal("the fixture starts folded")
	}
	edited := grouped()
	edited.Sections = map[string]config.DashboardSection{"databases": {Collapsed: true}}
	if err := config.Write(config.Config{Dashboard: edited}); err != nil {
		t.Fatal(err)
	}
	m.syncTiles()
	if !m.collapsed("databases") || shape(m) != "#databases [3] #edge [4] [5]" {
		t.Errorf("the edit did not arrive: folded %v, rows %s", m.collapsed("databases"), shape(m))
	}
}

// `]` on a header moves the section past the entry beside it, and the order it
// writes is the one the screen shows.
func TestMovingASectionByItsHeader(t *testing.T) {
	m := sectioned(t, listed("alpha", "one", "beta", "one", "gamma", "", "delta", "two"), 120, 60)
	m = selectHeader(m, "one")
	m = press(t, m, "]")
	if got := capIDs(m); got != "gamma alpha beta delta" || !m.onHeader("one") {
		t.Errorf("tiles = %s, on header %v", got, m.onHeader("one"))
	}
	if got := savedOrder(t); got != "gamma alpha beta delta" {
		t.Errorf("the file lists %s", got)
	}
	// Past a section, whole.
	m = press(t, m, "]")
	if got := capIDs(m); got != "gamma delta alpha beta" {
		t.Errorf("tiles = %s, want the section past the other one", got)
	}
	m = press(t, m, "]")
	if got := capIDs(m); got != "gamma delta alpha beta" || m.flash != "" {
		t.Errorf("at the end: %s, flash %q — the end of the dashboard is silent", got, m.flash)
	}
	m = press(t, m, "[")
	m = press(t, m, "[")
	m = press(t, m, "[")
	if got := capIDs(m); got != "alpha beta gamma delta" {
		t.Errorf("back to the top: %s", got)
	}
}

// `]` on a tile of a section that holds other entries moves the tile among them,
// and at the edge says why it stopped instead of letting the next build put it
// back.
func TestMovingATileInsideItsSection(t *testing.T) {
	m := sectioned(t, listed("alpha", "one", "beta", "one", "gamma", "one", "delta", ""), 120, 60)
	m.selected = 2 // beta
	m = press(t, m, "]")
	if got := capIDs(m); got != "alpha gamma beta delta" || m.selected != 3 {
		t.Fatalf("tiles = %s, selected %d", got, m.selected)
	}
	if got := savedOrder(t); got != "alpha gamma beta delta" {
		t.Errorf("the file lists %s", got)
	}
	m = press(t, m, "]")
	if got := capIDs(m); got != "alpha gamma beta delta" || m.selected != 3 {
		t.Errorf("the tile left its section: %s, selected %d", got, m.selected)
	}
	if m.flash != m.flashBad || !strings.Contains(m.flash, "last tile of one") || !strings.Contains(m.flash, "header") {
		t.Errorf("at the edge it said %q (refusal %q)", m.flash, m.flashBad)
	}
	if got := savedOrder(t); got != "alpha gamma beta delta" {
		t.Errorf("a refused move wrote %s", got)
	}
	m.flash, m.flashBad = "", ""
	m.selected = 1
	m = press(t, m, "[")
	if !strings.Contains(m.flash, "first tile of one") {
		t.Errorf("at the other edge it said %q", m.flash)
	}
}

// A tile in no section steps over a section whole, and a tile that is all its
// section holds moves the section, as an entry that expanded always moved its
// panels together.
func TestMovingPastASectionAndMovingASectionOfOne(t *testing.T) {
	m := sectioned(t, listed("alpha", "", "beta", "one", "gamma", "one", "delta", ""), 120, 60)
	m.selected = 1
	m = press(t, m, "]")
	if got := capIDs(m); got != "beta gamma alpha delta" || m.selected != 3 {
		t.Errorf("a tile in no section past a section: %s, selected %d", got, m.selected)
	}
	if got := savedOrder(t); got != "beta gamma alpha delta" {
		t.Errorf("the file lists %s", got)
	}

	m = sectioned(t, listed("alpha", "solo", "beta", "", "gamma", ""), 120, 60)
	m.selected = 1
	m = press(t, m, "]")
	if got := capIDs(m); got != "beta alpha gamma" || m.selected != 2 {
		t.Errorf("a section of one, from its tile: %s, selected %d", got, m.selected)
	}
	if m.flash != "saved the new order" {
		t.Errorf("flash = %q", m.flash)
	}
}

// A stated list keeps its entries before the added ones, and a section that
// takes entries from both is a move that cannot be written down when it would
// reorder across the seam.
func TestASectionDoesNotMoveAcrossTheStatedSeam(t *testing.T) {
	dash := config.Dashboard{
		Tiles: []config.Tile{{ID: "alpha.status"}, {ID: "beta.status"}},
		Add:   []config.Tile{{ID: "gamma.status", Section: "late"}},
	}
	m := sectioned(t, dash, 120, 60)
	m.selected = 2 // beta, the last stated entry
	before := capIDs(m)
	m = press(t, m, "]")
	if got := capIDs(m); got != before {
		t.Errorf("a stated entry moved past an added one: %s", got)
	}
	m = selectHeader(m, "late")
	m = press(t, m, "[")
	if got := capIDs(m); got != before {
		t.Errorf("an added section moved into the stated list: %s", got)
	}
}

// What was just added is on screen even when the group it joined starts folded.
func TestAddingATileUnfoldsTheGroupItLandsIn(t *testing.T) {
	m := pinnedModel(t, twoInstanceConfig(), config.Dashboard{
		Sections: map[string]config.DashboardSection{"db · prod": {Collapsed: true}},
	})
	c, _ := m.reg.Capability("db.status")
	next, _ := m.addTile(c, "prod")
	got := next.(Model)
	if got.collapsed("db · prod") {
		t.Errorf("the group the new tile is in is folded: %s", shape(got))
	}
	if got.selected < 1 || got.tiles[got.selected].section != "db · prod" {
		t.Errorf("selected %d, not on a panel of the group", got.selected)
	}
	if row, _ := got.rowAndCol(got.selected); row == -1 {
		t.Error("the selected tile is on no row")
	}
}

// A tile's own `z` action is still its own where nothing claims the key.
func TestATilesZActionIsReachableWhereTheDashboardHasNoSections(t *testing.T) {
	m := sectioned(t, listed("alpha", ""), 120, 40)
	m.selected = 1
	m.tiles[1].actions = []capAction{{key: "z", label: "zoom", cap: plugin.Capability{ID: "alpha.status", Safety: plugin.Read}}}
	if _, ok := m.selectedAction("z"); !ok {
		t.Fatal("the fixture's action is not there")
	}
	if got := m.offeredTileActions(1); len(got) != 1 {
		t.Errorf("offered %d actions, want the tile's z", len(got))
	}
	with := sectioned(t, grouped(), 120, 40)
	with.selected = 1
	with.tiles[1].actions = m.tiles[1].actions
	if got := with.offeredTileActions(1); len(got) != 0 {
		t.Errorf("a dashboard that folds with z offers a tile's z too: %v", got)
	}
}
