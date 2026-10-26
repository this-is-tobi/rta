package tui

import (
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// A stated list may hold many tiles of one key, a kube.pod.list for each
// namespace, and H on one of them took every entry of that key out of the file
// while only the one selected left the screen: the other stayed drawn until the
// next start, which drew neither and said nothing.
func TestHOnOneOfTwoStatedTilesOfOneKeyTakesOnlyItsOwnEntry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	dash := config.Dashboard{Tiles: []config.Tile{
		{ID: "zeta.inspect", With: map[string]any{"host": "a"}},
		{ID: "zeta.inspect", With: map[string]any{"host": "b"}},
	}}
	if err := config.Write(config.Config{Dashboard: dash}); err != nil {
		t.Fatal(err)
	}
	reg := multiRegistry(t)
	sized, _ := New(reg, dash, nil).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m := sized.(Model)
	m.selected = -1
	for i, tl := range m.tiles {
		if tl.source == tileStated && tl.values["host"] == "a" {
			m.selected = i
		}
	}
	if m.selected < 1 || len(m.tiles) != 3 {
		t.Fatalf("tiles %v, want the search bar and the two stated ones", tileKeys(m.tiles))
	}

	m.hideSelected()

	saved := savedConfig(t).Dashboard.Tiles
	if len(saved) != 1 || saved[0].With["host"] != "b" {
		t.Fatalf("the file holds %+v, want the entry for b alone", saved)
	}
	var drawn []any
	for _, tl := range buildTiles(reg, savedConfig(t).Dashboard)[1:] {
		drawn = append(drawn, tl.values["host"])
	}
	if len(drawn) != 1 || drawn[0] != "b" {
		t.Errorf("the next start draws %v, want the tile for b", drawn)
	}
	if len(m.tiles) != 2 || m.tiles[1].values["host"] != "b" {
		t.Errorf("the screen holds %v, want the tile for b", tileKeys(m.tiles))
	}
}

// Two stated entries may ask the same thing and differ in how they are drawn,
// both on screen. H took the last entry of the key that asked the same thing,
// whichever of the two was selected, so the next start brought back the panel
// that was hidden and lost the one that was kept.
func TestHOnOneOfTwoStatedTilesThatAskTheSameTakesTheSelectedOne(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	dash := config.Dashboard{Tiles: []config.Tile{
		{ID: "zeta.inspect", With: map[string]any{"host": "a"}},
		{ID: "zeta.inspect", With: map[string]any{"host": "a"}, Width: 2},
	}}
	if err := config.Write(config.Config{Dashboard: dash}); err != nil {
		t.Fatal(err)
	}
	sized, _ := New(multiRegistry(t), dash, nil).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m := sized.(Model)
	m.selected = -1
	for i, tl := range m.tiles {
		if tl.source == tileStated && !tl.widthDemand {
			m.selected = i
		}
	}
	if m.selected < 1 || len(m.tiles) != 3 {
		t.Fatalf("tiles %v, want the search bar and the two stated ones", tileKeys(m.tiles))
	}

	m.hideSelected()

	saved := savedConfig(t).Dashboard.Tiles
	if len(saved) != 1 || saved[0].Width != 2 {
		t.Fatalf("the file holds %+v, want the entry two columns wide alone", saved)
	}
}
