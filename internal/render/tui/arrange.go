package tui

import (
	"errors"
	"fmt"

	"github.com/this-is-tobi/rta/internal/config"
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

// visibleKeys is the current arrangement as tile keys, search excluded.
func (m Model) visibleKeys() []string {
	out := make([]string, 0, len(m.tiles))
	for _, t := range m.tiles[1:] {
		out = append(out, t.key())
	}
	return out
}

// hideSelected takes the selected tile off the dashboard and persists it —
// by hiding an automatic tile, and by withdrawing an entry somebody wrote.
// A stated or added entry has no notion of "hidden": left in its list, the
// tile would reappear on the next run and the hide would look broken, and
// an `add:` entry is the person's own ask, so the honest record of taking
// it down is the entry being gone.
func (m *Model) hideSelected() string {
	if m.selected < 1 || m.selected >= len(m.tiles) {
		return ""
	}
	gone := m.tiles[m.selected]
	var note string
	switch {
	case gone.expanded:
		// One panel of several its entry became: hidden by its key, so its
		// siblings stay and a connection added to the profile later still
		// gets its panel. Withdrawing the entry would take them all down.
		m.dash.Hidden = append(m.dash.Hidden, gone.key())
		note = fmt.Sprintf("hid %s — `rta dashboard unhide %s --profile %s` brings it back",
			gone.key(), gone.cap.ID, gone.profile)
	case gone.source == tileStated:
		m.dash.Tiles = dropTile(m.dash.Tiles, gone.key())
		note = fmt.Sprintf("removed %s from the stated dashboard", gone.key())
	case gone.source == tileAdded:
		m.dash.Add = dropTile(m.dash.Add, gone.key())
		// Say how it comes back, as the exact command: an undo that is
		// "find the config file" is one people are right to be nervous
		// about pressing toward.
		note = fmt.Sprintf("removed %s — `%s` puts it back", gone.key(), addCommand(gone))
	default:
		m.dash.Hidden = append(m.dash.Hidden, gone.cap.ID)
		note = fmt.Sprintf("hid %s — press p to bring it back", gone.cap.ID)
	}
	m.tiles = append(m.tiles[:m.selected], m.tiles[m.selected+1:]...)
	m.selected = min(m.selected, len(m.tiles)-1)
	m.clampScroll()

	if err := m.save(); err != nil {
		return note + " (this session only: " + err.Error() + ")"
	}
	return note
}

// addCommand is the `rta dashboard add` line that would write this tile's
// entry again.
func addCommand(t tile) string {
	cmd := "rta dashboard add " + t.cap.ID
	if t.profile != "" {
		cmd += " --profile " + t.profile
	}
	return cmd
}

// moveSelected shifts the selected tile by one position and persists the
// resulting order.
func (m *Model) moveSelected(delta int) string {
	target := m.selected + delta
	if m.selected < 1 || target < 1 || target >= len(m.tiles) {
		return ""
	}
	// A stated list keeps its own order and the added entries follow it
	// (buildTiles), so a move across that seam would show an order the file
	// cannot reproduce on the next run. The seam is an end, like the edges.
	if len(m.dash.Tiles) > 0 && m.tiles[m.selected].source != m.tiles[target].source {
		return ""
	}
	m.tiles[m.selected], m.tiles[target] = m.tiles[target], m.tiles[m.selected]
	m.selected = target
	m.clampScroll()

	// Record the whole visible order, not just the pair that moved: a
	// partial order would leave the rest to drift on the next run, and what
	// you see is what you asked for. The written lists follow it too, each
	// within itself, so the file reads in the order the screen shows.
	m.dash.Order = m.visibleKeys()
	m.dash.Tiles = reorderTiles(m.dash.Tiles, m.dash.Order)
	m.dash.Add = reorderTiles(m.dash.Add, m.dash.Order)
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
	// Refused on a path nobody named, and this is the half of the dashboard
	// gate that is not about reading. app.go draws config.TrustedDashboard(),
	// so on such a path m.dash holds the automatic arrangement rather than
	// whatever the file states — and this function copies m.dash *back into
	// the file*. Without the refusal, gating the read turns one `h` keypress
	// into "erase the dashboard: block somebody wrote", which is a worse
	// outcome than the hole the gate closes.
	if !config.TrustedPath() {
		return errors.New(config.Path() + " is not a config file rta honours, so the " +
			"arrangement was not written — set $RTA_CONFIG to name it deliberately")
	}
	return config.Mutate(func(cfg config.Config) (config.Config, bool) {
		cfg.Dashboard.Hidden = m.dash.Hidden
		cfg.Dashboard.Order = m.dash.Order
		cfg.Dashboard.Tiles = m.dash.Tiles
		cfg.Dashboard.Add = m.dash.Add
		return cfg, true
	})
}

// dropTile removes every entry with the given key (config.Tile.Key).
func dropTile(tiles []config.Tile, key string) []config.Tile {
	if len(tiles) == 0 {
		return tiles
	}
	out := make([]config.Tile, 0, len(tiles))
	for _, t := range tiles {
		if t.Key() != key {
			out = append(out, t)
		}
	}
	return out
}

// reorderTiles rewrites a written tile list into the given order of keys,
// keeping each entry's configured inputs.
//
// Entries sharing a key are kept, in their own order, at the key's place:
// a list naming obj.get twice against two hosts is two tiles on screen and
// two entries here, and an index by key alone kept one of them and lost
// the other on the first move.
func reorderTiles(tiles []config.Tile, order []string) []config.Tile {
	if len(tiles) == 0 {
		return tiles
	}
	byKey := make(map[string][]config.Tile, len(tiles))
	for _, t := range tiles {
		byKey[t.Key()] = append(byKey[t.Key()], t)
	}
	out := make([]config.Tile, 0, len(tiles))
	for _, key := range order {
		if ts, ok := byKey[key]; ok {
			out = append(out, ts...)
			delete(byKey, key)
		}
	}
	// Anything the dashboard did not show (an ID no longer in the registry)
	// stays in the file rather than being silently dropped.
	for _, t := range tiles {
		if ts, unplaced := byKey[t.Key()]; unplaced {
			out = append(out, ts...)
			delete(byKey, t.Key())
		}
	}
	return out
}
