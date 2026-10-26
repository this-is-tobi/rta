package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// kv.list declares c for copying a row's value (kv.copy) and no copy of its
// own, which a capability cannot have both of. On the dashboard c answered a
// declared copy and nothing else, so on the kv.list tile the bar said `c copy`
// and the key did nothing at all. It is the tile's action where the tile has
// no copy, as it is on the page.
func TestCOnATileWhoseActionIsCRunsTheAction(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(dir, "config.yaml"))
	t.Setenv("RTA_DATA_DIR", dir)
	sized, _ := New(realRegistry(t), config.Dashboard{Tiles: []config.Tile{{ID: "kv.list"}}}, nil).
		Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m := sized.(Model)
	m.selected = 1
	if m.tiles[1].cap.ID != "kv.list" {
		t.Fatalf("tile 1 is %q, want kv.list", m.tiles[1].cap.ID)
	}
	taught := ""
	for _, it := range m.footerItems(modeDashboard) {
		for _, k := range it.keys {
			if k == "c" {
				taught = it.label
			}
		}
	}
	if taught != "copy" {
		t.Fatalf("the bar teaches c as %q, want the tile's copy", taught)
	}
	got := press(t, m, "c")
	if got.current.ID != "kv.list" || !strings.Contains(got.flash, "pick a row, then c copy") {
		t.Errorf("c on the tile opened %q with %q, want the list and what to do on it", got.current.ID, got.flash)
	}
}
