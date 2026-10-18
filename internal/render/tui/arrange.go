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
	// A header is not a tile, and what H would mean on one is not one thing: the
	// tiles under it came from `tiles:`, from `add:` and from the automatic set,
	// each taken down in its own way with its own way back, and a section is
	// five of them at once. One press that wrote all of that to the file would be
	// the most destructive key on the screen sitting beside the one that folds, so
	// it says what to press instead.
	if m.selected < 0 && m.header != "" {
		m.refuse("a section is folded with z, and H hides one tile at a time — select a tile under " + sectionLabel(m.header))
		return m.flash
	}
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
		way := addedEntry(m.dash.Add, gone).AddCommand(m.view)
		m.dash.Add = dropTile(m.dash.Add, gone.key())
		// Say how it comes back, as the exact command: an undo that is
		// "find the config file" is one people are right to be nervous
		// about pressing toward.
		note = fmt.Sprintf("removed %s — `%s` puts it back", gone.key(), way)
		// An entry that stood in the automatic tile's place: with the
		// entry gone that tile is back on the next build, and H meant off
		// the screen, so the tile is hidden as well — p shows it the way
		// it shows any hidden automatic tile.
		if m.standsForAutomatic(gone) {
			if !slices.Contains(m.dash.Hidden, gone.cap.ID) {
				m.dash.Hidden = append(m.dash.Hidden, gone.cap.ID)
			}
			note = fmt.Sprintf("removed %s and hid the automatic tile it stood for — `%s` puts it back as it was, p the plain one",
				gone.key(), way)
		}
	default:
		m.dash.Hidden = append(m.dash.Hidden, gone.cap.ID)
		note = fmt.Sprintf("hid %s — press p to bring it back", gone.cap.ID)
	}
	m.tiles = append(m.tiles[:m.selected], m.tiles[m.selected+1:]...)
	// Where the selection goes may be a tile that is folded away, which is not
	// somewhere it can rest.
	m.keepSelection()
	m.clampScroll()

	if err := m.save(); err != nil {
		return m.refused("not saved: " + note + " (this session only: " + err.Error() + ")")
	}
	return note
}

// withdraws reports whether H on this tile takes an entry out of the file
// rather than writing a `hidden:` line — the split hideSelected's switch
// makes, for the footer, which used to call both of them "hide". The word is
// not cosmetic: the inventory pane brings back a plugin's automatic tile from
// `hidden:` and nothing else, so a person who read "hide", pressed it on
// their own added tile and went to p for it found no row there. The way back
// is the `rta dashboard add` line the note prints, or + on a catalogue row.
// An expanded entry's panel is hidden, and the footer says so, but by a panel
// key p has no row for either: its way back is the `rta dashboard unhide`
// line hideSelected's note prints.
func (t tile) withdraws() bool {
	return !t.expanded && (t.source == tileStated || t.source == tileAdded)
}

// addedEntry is the `add:` entry the added tile t was built from, as the file
// states it: the last one of its key when several share it, which is the one on
// screen (joinTiles) and the one `rta dashboard rm` reports. The way back is
// spelled from that entry and not from the tile, which holds what the layout
// needs of it — a section, for one, that an expanded panel is given by rule and
// the entry never stated — so a field the entry has is in the way back without
// this function knowing it, which is what the CLI's receipt does with the same
// entry.
func addedEntry(entries []config.Tile, t tile) config.Tile {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Key() == t.key() {
			return entries[i]
		}
	}
	return config.Tile{ID: t.cap.ID, Profile: t.profile}
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

// moveSelected shifts the selected tile, or the section whose header is
// selected, by one position and persists the resulting order. A panel of several
// that one entry became moves with its siblings, past whatever sits beyond them:
// the file can place the entry and nothing finer, so a move that split them, or
// reordered them among themselves, would show an order the next build could not
// reproduce.
//
// **A section is one thing to move, and a tile in one moves inside it.** The file
// places a section where its first tile is, so moving a section is moving
// entries past it, and a tile that stepped out of its section would be put back
// by the next build, since the section it names draws all its tiles together. So
// `[` and `]` on a header move the section past the entry or the section beside
// it; on a tile of a section that holds other entries they move the tile among
// them, and at the edge say so instead of doing nothing; and on a tile that is
// all its section holds, the section is what moves, which is what the same key
// did for an entry that expanded into several panels before sections were
// drawn. A tile in no section steps over a whole section as it does over a tile.
//
// Putting a tile in another section is not something these keys do: the section
// is a property of the entry, and an automatic tile has no entry to write it
// to. `rta dashboard add <capability> --section <name>` states it.
func (m *Model) moveSelected(delta int) string {
	onHeader := m.selected < 0 && m.header != ""
	sel := m.selected
	if onHeader {
		run, ok := m.sectionNamed(m.header)
		if !ok {
			return ""
		}
		sel = run.lo
	}
	if sel < 1 || sel >= len(m.tiles) {
		return ""
	}
	// The two runs to exchange, [a, b) and [b, c): the selected thing and the one
	// beside it, each one tile unless it is an entry that expanded or a section.
	lo, hi, from, to, inner := m.movingUnit(sel, onHeader)
	beside := m.unitSpan
	if inner {
		beside = m.entrySpan
	}
	var a, b, c int
	if delta < 0 {
		if lo <= from {
			return m.atTheEdge(sel, "first", inner)
		}
		a, _ = beside(lo - 1)
		b, c = lo, hi
	} else {
		if hi >= to {
			return m.atTheEdge(sel, "last", inner)
		}
		a, b = lo, hi
		_, c = beside(hi)
	}
	next := swapRuns(m.tiles, a, b, c)
	// A stated list keeps its own order and the added entries follow it
	// (buildTiles), so a move across that seam would show an order the file
	// cannot reproduce on the next run. The seam is an end, like the edges.
	if len(m.dash.Tiles) > 0 && !readsBackAsIs(next) {
		return ""
	}
	m.tiles = next
	if !onHeader {
		if delta < 0 {
			m.selected -= b - a
		} else {
			m.selected += c - b
		}
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
		return m.refused("not saved: reordered (this session only: " + err.Error() + ")")
	}
	return "saved the new order"
}

// entrySpan is the run of tiles around i that stand for one entry, as
// [lo, hi): the tile alone, unless it is one panel of several. Two entries
// that differ only in the section they name are two entries, and so are never
// one run, although they share a key.
func (m Model) entrySpan(i int) (lo, hi int) {
	same := func(j int) bool {
		return m.tiles[j].entryKey() == m.tiles[i].entryKey() && m.tiles[j].section == m.tiles[i].section
	}
	lo, hi = i, i+1
	for lo > 1 && same(lo-1) {
		lo--
	}
	for hi < len(m.tiles) && same(hi) {
		hi++
	}
	return lo, hi
}

// unitSpan is what steps as one thing past a tile: the whole of its section, or
// its entry when it is in none.
func (m Model) unitSpan(i int) (lo, hi int) {
	if run, ok := m.sectionNamed(m.tiles[i].section); ok {
		return run.lo, run.hi
	}
	return m.entrySpan(i)
}

// sectionNamed is the run of the section called name, if it is on the dashboard.
func (m Model) sectionNamed(name string) (sectionRun, bool) {
	if name == "" {
		return sectionRun{}, false
	}
	for _, r := range m.sectionRuns() {
		if r.name == name {
			return r, true
		}
	}
	return sectionRun{}, false
}

// movingUnit is the run [lo, hi) that moves when the selection does, and the
// room [from, to) it moves in. Inside is true when that room is one section and
// the run is an entry in it: the tile of a section that holds other entries.
func (m Model) movingUnit(sel int, onHeader bool) (lo, hi, from, to int, inner bool) {
	run, inSection := m.sectionNamed(m.tiles[sel].section)
	if !inSection {
		lo, hi = m.entrySpan(sel)
		return lo, hi, 1, len(m.tiles), false
	}
	lo, hi = m.entrySpan(sel)
	if onHeader || (lo == run.lo && hi == run.hi) {
		return run.lo, run.hi, 1, len(m.tiles), false
	}
	return lo, hi, run.lo, run.hi, true
}

// atTheEdge is what a move that has nowhere to go says. Silent at the ends of the
// dashboard, as it always was; at the end of a section it says why, since the key
// worked a press ago and what is different is the section.
func (m *Model) atTheEdge(sel int, end string, inner bool) string {
	if !inner {
		return ""
	}
	t := m.tiles[sel]
	m.refuse(fmt.Sprintf("%s is the %s tile of %s — [ ] on its header moves the section", t.key(), end, sectionLabel(t.section)))
	return m.flash
}

// readsBackAsIs reports whether a stated dashboard written in this order is built
// into this order: the stated entries first and the added ones after, and each
// section together where its first tile is.
func readsBackAsIs(tiles []tile) bool {
	var stated, added []tile
	for _, t := range tiles[1:] {
		if t.source == tileStated {
			stated = append(stated, t)
		} else {
			added = append(added, t)
		}
	}
	want := groupSections(append(stated, added...))
	return slices.EqualFunc(want, tiles[1:], func(a, b tile) bool { return a.key() == b.key() && a.section == b.section })
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
		block, _ := cfg.Block(m.view)
		block.Hidden = m.dash.Hidden
		block.Order = m.dash.Order
		block.Tiles = m.dash.Tiles
		block.Add = m.dash.Add
		cfg.SetBlock(m.view, block)
		return cfg, true
	}); err != nil {
		return err
	}
	// What the file says now is what this session wrote, so the next tick
	// reads it as its own rather than as an edit to adopt (syncDashboard).
	m.dashOnDisk = dashStamp(m.view, m.dash)
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
