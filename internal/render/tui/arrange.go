package tui

import (
	"errors"
	"fmt"
	"slices"

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

// visibleEntries is the current arrangement as the keys `order:` takes,
// search excluded: one per entry, so the panels one entry expanded into
// count once, at the place the first of them holds.
func (m Model) visibleEntries() []string {
	out := make([]string, 0, len(m.tiles))
	seen := map[string]bool{}
	for _, t := range m.tiles[1:] {
		if key := t.entryKey(); !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
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
		// An entry that stood in the automatic tile's place: with the
		// entry gone that tile is back on the next build, and H meant off
		// the screen, so the tile is hidden as well — p shows it the way
		// it shows any hidden automatic tile.
		if m.standsForAutomatic(gone) {
			if !slices.Contains(m.dash.Hidden, gone.cap.ID) {
				m.dash.Hidden = append(m.dash.Hidden, gone.cap.ID)
			}
			note = fmt.Sprintf("removed %s and hid the automatic tile it stood for — `%s` puts it back as it was, p the plain one",
				gone.key(), addCommand(gone))
		}
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
	entry := config.Tile{ID: t.cap.ID, Profile: t.profile, Span: t.span, With: t.values}
	return "rta dashboard add " + entry.AddArgs()
}

// standsForAutomatic reports whether an added tile took an automatic
// tile's place (joinTiles).
func (m Model) standsForAutomatic(t tile) bool {
	return t.source == tileAdded && m.automaticKey(t.key())
}

// automaticKey reports whether the automatic set produces a tile of this
// key — there being an automatic set, which a stated list replaces.
func (m Model) automaticKey(key string) bool {
	if len(m.dash.Tiles) > 0 {
		return false
	}
	for _, auto := range autoTiles(m.reg) {
		if auto.key() == key {
			return true
		}
	}
	return false
}

// moveSelected shifts the selected tile by one position and persists the
// resulting order. A panel of several that one entry became moves with its
// siblings, past whatever sits beyond them: the file can place the entry
// and nothing finer, so a move that split them, or reordered them among
// themselves, would show an order the next build could not reproduce.
func (m *Model) moveSelected(delta int) string {
	if m.selected < 1 || m.selected >= len(m.tiles) {
		return ""
	}
	// The two runs to exchange, [a, b) and [b, c): the selected tile's
	// entry and the entry beside it, each one tile unless it expanded.
	lo, hi := m.entrySpan(m.selected)
	var a, b, c int
	if delta < 0 {
		if lo <= 1 {
			return ""
		}
		a, _ = m.entrySpan(lo - 1)
		b, c = lo, hi
	} else {
		if hi >= len(m.tiles) {
			return ""
		}
		a, b = lo, hi
		_, c = m.entrySpan(hi)
	}
	// A stated list keeps its own order and the added entries follow it
	// (buildTiles), so a move across that seam would show an order the file
	// cannot reproduce on the next run. The seam is an end, like the edges.
	if len(m.dash.Tiles) > 0 && m.tiles[a].source != m.tiles[b].source {
		return ""
	}
	m.tiles = swapRuns(m.tiles, a, b, c)
	if delta < 0 {
		m.selected -= b - a
	} else {
		m.selected += c - b
	}
	m.clampScroll()

	// Record the whole visible order, not just the pair that moved: a
	// partial order would leave the rest to drift on the next run, and what
	// you see is what you asked for. The written lists follow it too, each
	// within itself, so the file reads in the order the screen shows.
	m.dash.Order = m.visibleEntries()
	m.dash.Tiles = reorderTiles(m.dash.Tiles, m.dash.Order)
	m.dash.Add = reorderTiles(m.dash.Add, m.dash.Order)
	if err := m.save(); err != nil {
		return "reordered (this session only: " + err.Error() + ")"
	}
	return "saved the new order"
}

// entrySpan is the run of tiles around i that stand for one entry, as
// [lo, hi): the tile alone, unless it is one panel of several.
func (m Model) entrySpan(i int) (lo, hi int) {
	key := m.tiles[i].entryKey()
	lo, hi = i, i+1
	for lo > 1 && m.tiles[lo-1].entryKey() == key {
		lo--
	}
	for hi < len(m.tiles) && m.tiles[hi].entryKey() == key {
		hi++
	}
	return lo, hi
}

// swapRuns exchanges the runs [a, b) and [b, c) of tiles.
func swapRuns(tiles []tile, a, b, c int) []tile {
	out := make([]tile, 0, len(tiles))
	out = append(out, tiles[:a]...)
	out = append(out, tiles[b:c]...)
	out = append(out, tiles[a:b]...)
	return append(out, tiles[c:]...)
}

// save writes the arrangement back, leaving the rest of the config alone:
// the dashboard is one part of the file, and moving a tile must not rewrite
// anything else — including this shell's RTA_* environment, which is why it
// re-reads the file rather than the resolved config (config.LoadFile).
func (m *Model) save() error {
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
	if err := config.Mutate(func(cfg config.Config) (config.Config, bool) {
		cfg.Dashboard.Hidden = m.dash.Hidden
		cfg.Dashboard.Order = m.dash.Order
		cfg.Dashboard.Tiles = m.dash.Tiles
		cfg.Dashboard.Add = m.dash.Add
		return cfg, true
	}); err != nil {
		return err
	}
	// What the file says now is what this session wrote, so the next tick
	// reads it as its own rather than as an edit to adopt (syncDashboard).
	m.dashOnDisk = dashStamp(m.dash)
	return nil
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
