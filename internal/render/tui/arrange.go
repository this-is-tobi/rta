package tui

import (
	"fmt"

	"github.com/this-is-tobi/rule-them-all/internal/config"
)

// Rearranging the dashboard from inside the dashboard.
//
// The landing screen shows every plugin, which is the right default and also
// more than most people want at once. Curating it should not mean quitting,
// finding the config file, and guessing capability IDs — the arrangement is
// a visual thing and belongs where you can see it.
//
// Edits write straight through to the config file, so the next run opens on
// what you left. They are stored as adjustments to the automatic set (hide
// these, lead with those) rather than as a frozen list, which is what keeps
// a plugin installed next month from being invisible because you once moved
// a tile.

// visibleIDs is the current arrangement as capability IDs, search excluded.
func (m Model) visibleIDs() []string {
	out := make([]string, 0, len(m.tiles))
	for _, t := range m.tiles[1:] {
		out = append(out, t.cap.ID)
	}
	return out
}

// hideSelected removes the selected tile from the dashboard and persists it.
func (m *Model) hideSelected() string {
	if m.selected < 1 || m.selected >= len(m.tiles) {
		return ""
	}
	hidden := m.tiles[m.selected]
	m.dash.Hidden = append(m.dash.Hidden, hidden.cap.ID)
	// An explicit tile list has no notion of "hidden": edit it in place, or
	// the tile would reappear on the next run and the hide would look broken.
	m.dash.Tiles = dropTile(m.dash.Tiles, hidden.cap.ID)
	m.tiles = append(m.tiles[:m.selected], m.tiles[m.selected+1:]...)
	m.selected = min(m.selected, len(m.tiles)-1)
	m.clampScroll()

	note := fmt.Sprintf("hid %s", hidden.cap.ID)
	if err := m.save(); err != nil {
		return note + " (this session only: " + err.Error() + ")"
	}
	// Say where it went. A hide whose undo is "find the config file" is a
	// hide people are right to be nervous about pressing.
	return note + " — press p to bring it back"
}

// moveSelected shifts the selected tile by one position and persists the
// resulting order.
func (m *Model) moveSelected(delta int) string {
	target := m.selected + delta
	if m.selected < 1 || target < 1 || target >= len(m.tiles) {
		return ""
	}
	m.tiles[m.selected], m.tiles[target] = m.tiles[target], m.tiles[m.selected]
	m.selected = target
	m.clampScroll()

	// Record the whole visible order, not just the pair that moved: a
	// partial order would leave the rest to drift on the next run, and what
	// you see is what you asked for.
	m.dash.Order = m.visibleIDs()
	if len(m.dash.Tiles) > 0 {
		m.dash.Tiles = reorderTiles(m.dash.Tiles, m.dash.Order)
	}
	if err := m.save(); err != nil {
		return "reordered (this session only: " + err.Error() + ")"
	}
	return "saved the new order"
}

// save writes the arrangement back, leaving the rest of the config alone:
// the dashboard is one part of the file, and moving a tile must not rewrite
// anything else — including this shell's RTA_* environment, which is why it
// re-reads the file rather than the resolved config (config.LoadFile).
func (m Model) save() error {
	cfg, err := config.LoadFile()
	if err != nil {
		return err
	}
	cfg.Dashboard.Hidden = m.dash.Hidden
	cfg.Dashboard.Order = m.dash.Order
	cfg.Dashboard.Tiles = m.dash.Tiles
	return config.Write(cfg)
}

func dropTile(tiles []config.Tile, id string) []config.Tile {
	if len(tiles) == 0 {
		return tiles
	}
	out := make([]config.Tile, 0, len(tiles))
	for _, t := range tiles {
		if t.ID != id {
			out = append(out, t)
		}
	}
	return out
}

// reorderTiles rewrites an explicit tile list into the given order, keeping
// each entry's configured inputs.
func reorderTiles(tiles []config.Tile, order []string) []config.Tile {
	byID := make(map[string]config.Tile, len(tiles))
	for _, t := range tiles {
		byID[t.ID] = t
	}
	out := make([]config.Tile, 0, len(tiles))
	for _, id := range order {
		if t, ok := byID[id]; ok {
			out = append(out, t)
			delete(byID, id)
		}
	}
	// Anything the dashboard did not show (an ID no longer in the registry)
	// stays in the file rather than being silently dropped.
	for _, t := range tiles {
		if _, unplaced := byID[t.ID]; unplaced {
			out = append(out, t)
			delete(byID, t.ID)
		}
	}
	return out
}
