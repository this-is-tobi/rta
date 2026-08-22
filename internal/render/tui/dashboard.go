package tui

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/cli"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A tile is a capability previewed as a dashboard pane. Tiles are pure
// composition — capability ID + values in, View out — the same data-driven
// shape a future a2tea integration would consume (PROJECT.md §6.4). Enter or
// a mouse click opens the full result.
type tile struct {
	cap    plugin.Capability
	values map[string]any
	// span is how many grid columns this tile occupies, 0 meaning "work it
	// out from the capability's MinWidth". It replaces a bool that could
	// only say "one column or all of them": on a four-column screen that
	// gave a 44-character key the whole 200 cells, which is as wrong in the
	// other direction as cramming it into 39.
	span int
	view view.View
	err  *view.Error
	// search marks the live search tile: a full-width query bar over the
	// registry, always first.
	search bool
	// actions are one-key shortcuts offered while this tile is selected —
	// the dashboard's buttons (add/edit/done on the todo tile).
	actions []capAction
}

// actionSource says where an action gets the identity of the record it acts
// on — the one thing that differs between acting from a list and acting from
// the page of a single record.
type actionSource int

const (
	srcNone actionSource = iota // no subject: "add" needs nobody's id
	srcRow                      // the selected table row (its first column)
	srcSelf                     // the record the current view is already about
)

// capAction opens a sibling capability from the view you are looking at: a
// button on a dashboard tile, a row action inside a result table, or an
// action on the detail page of one record.
type capAction struct {
	key   string
	label string
	cap   plugin.Capability
	src   actionSource
}

// capActionSpecs declares which capabilities each view can reach in one key.
// One table drives every surface: dashboard tile buttons (minus "enter",
// which opens the tile), row actions inside result tables, and the actions
// on a record's own page — so a note is as editable as a task, wherever you
// happen to be looking at it. Keys must not collide with navigation
// (hjkl/arrows, tab, b, :, /, q) or result keys (r, y, c) — "c" here means
// this table's own row-action copy (kv.list/kv.show → kv.copy); it is also
// the key copyvalue.go's copySpecs uses for a capability with no sibling
// action to copy through, checked both on a result already open
// (resultView) and directly against a tile's own preview (dashFooter,
// tui.go's modeDashboard "c" case). A capability must not appear in both
// tables, or whichever one this loop or that case reaches first shadows
// the other's hint silently — today that would require a capability that
// both backs a tile (its own "overview") and declares a capActionSpecs "c"
// entry for itself, which none currently does.
var capActionSpecs = map[string][]struct {
	key, label, id string
	src            actionSource
}{
	"todo.list": {
		{"enter", "show", "todo.show", srcRow},
		{"a", "add", "todo.add", srcNone},
		{"e", "edit", "todo.edit", srcRow},
		{"d", "done", "todo.done", srcRow},
		// The undo for `d`, one key away from it: marking the wrong task
		// complete is a one-keystroke mistake and should cost one keystroke
		// to take back.
		{"o", "re-open", "todo.reopen", srcRow},
		{"x", "remove", "todo.rm", srcRow},
	},
	"note.list": {
		{"enter", "show", "note.show", srcRow},
		{"a", "add", "note.add", srcNone},
		{"e", "edit", "note.edit", srcRow},
		{"x", "remove", "note.rm", srcRow},
	},
	// The hosts file is a list you manage, not just read: park an override
	// with `t`, drop it with `x`. `t` rather than `d` — d is "done" on the
	// task lists, and a key that means two things across two screens is a
	// key you hesitate over.
	"net.hosts.list": {
		{"a", "add", "net.hosts.add", srcNone},
		{"t", "toggle", "net.hosts.toggle", srcRow},
		{"x", "remove", "net.hosts.rm", srcRow},
	},
	// A stale grant is something you notice on the dashboard, so taking it
	// back has to be possible from there and not only from a shell. `e`
	// re-issues the grant under the cursor, which is what "it expired while I
	// was still using it" actually needs.
	"grant.list": {
		{"a", "allow", "grant.allow", srcNone},
		{"e", "renew", "grant.allow", srcRow},
		{"x", "revoke", "grant.revoke", srcRow},
	},
	// Deliberately no "reveal" action: a secret shown because a key was
	// pressed on a list is a secret shown by accident. `kv get` asks for it
	// by name, which is the point at which you meant to. Enter opens the
	// entry's metadata instead, which is everything about it that is safe to
	// put on a screen.
	//
	// `c` is not that decision quietly reversed. A copy puts nothing on the
	// screen, so it leaves none of what a reveal leaves — no scrollback, no
	// screen share, no photograph of somebody's monitor, no asciinema
	// recording — and the next thing anybody copies undoes it, while a
	// revealed secret stays revealed for as long as the buffer lives. It is
	// also not one keystroke: the passphrase and identity are inputs like any
	// other, so every kv action opens the unlock form on the way.
	//
	// kv.edit is absent for the opposite reason to the missing reveal: it
	// hands the terminal to $EDITOR, and the terminal is what this program
	// is drawing on.
	"kv.list": {
		{"enter", "show", "kv.show", srcRow},
		{"c", "copy", "kv.copy", srcRow},
		{"a", "add", "kv.set", srcNone},
		{"e", "set", "kv.set", srcRow},
		{"m", "rename", "kv.rename", srcRow},
		{"x", "remove", "kv.rm", srcRow},
	},
	// The kv tile is `kv status`, which is about the store rather than any
	// entry — so its actions are the two things you want from there: the
	// list, and a new secret.
	"kv.status": {
		{"s", "secrets", "kv.list", srcNone},
		{"a", "add", "kv.set", srcNone},
	},
	"kv.show": {
		{"c", "copy", "kv.copy", srcSelf},
		{"e", "set", "kv.set", srcSelf},
		{"m", "rename", "kv.rename", srcSelf},
		{"x", "remove", "kv.rm", srcSelf},
		{"a", "add", "kv.set", srcNone},
	},
	// The detail pages act on the record they are already showing.
	"todo.show": {
		{"e", "edit", "todo.edit", srcSelf},
		{"d", "done", "todo.done", srcSelf},
		{"o", "re-open", "todo.reopen", srcSelf},
		{"x", "remove", "todo.rm", srcSelf},
		{"a", "add", "todo.add", srcNone},
	},
	"note.show": {
		{"e", "edit", "note.edit", srcSelf},
		{"x", "remove", "note.rm", srcSelf},
		{"a", "add", "note.add", srcNone},
	},
}

// viewToggle flips one boolean input of the view you are already looking at.
//
// It is not an action: nothing else runs and nowhere else opens. It is the
// filter on the list in front of you, which is a different thing and needs a
// different mechanism — `todo.list` hides completed tasks, so without this
// the re-open action could never find a row to act on. A capability that
// hides part of its own data by default owes the surface a way to ask for
// the rest.
type viewToggle struct {
	key, label, field string
}

var viewToggleSpecs = map[string][]viewToggle{
	"todo.list": {{key: "A", label: "show done", field: "all"}},
	"kv.list":   {{key: "D", label: "detail", field: "detail"}},
}

// toggleFor resolves a key to a toggle declared for this capability.
func toggleFor(capID, key string) (viewToggle, bool) {
	for _, t := range viewToggleSpecs[capID] {
		if t.key == key {
			return t, true
		}
	}
	return viewToggle{}, false
}

// capActions resolves the declared actions for a capability against the
// registry. Unknown IDs simply do not appear.
func capActions(reg *registry.Registry, capID string) []capAction {
	var out []capAction
	for _, spec := range capActionSpecs[capID] {
		if c, ok := reg.Capability(spec.id); ok {
			out = append(out, capAction{key: spec.key, label: spec.label, cap: c, src: spec.src})
		}
	}
	return out
}

const (
	// tileRefreshInterval paces dashboard refreshes.
	tileRefreshInterval = 5 * time.Second
	// tileHeight is the maximum panel height: the title lives in the top
	// border, so the body gets up to tileHeight-2 preview lines before a tile
	// truncates with "… enter for details" — unchanged from before rows grew
	// responsive, so nothing that used to fit fully still doesn't.
	tileHeight = 11
	// tilePreviewLines is the tile body capacity at the maximum height.
	tilePreviewLines = tileHeight - 2
	// tileMinHeight is the floor a row shrinks to when every tile in it is
	// short: small enough to reclaim real space from a one-line KeyValue or
	// "nothing active" message, tall enough that a panel still reads as a
	// panel next to a taller row rather than a sliver. Tiles within one row
	// always share that row's height — this only ever varies row to row.
	tileMinHeight = 6
	// searchMatches is how many live results the search bar shows at once.
	// The list itself is not cut to this: it scrolls, because a query that
	// matches eleven capabilities should not silently become three.
	searchMatches = 3
	// searchTileHeight: borders + query line + searchMatches result lines.
	searchTileHeight = searchMatches + 3
)

// pluginOrder is the shipped arrangement: what you glance at most, first.
// Plugins not named here follow, alphabetically — a plugin installed later
// lands on the dashboard without anyone editing this list.
var pluginOrder = []string{"todo", "note", "sys", "net", "kv", "grant"}

// preferredTile overrides the tile convention for a plugin whose best glance
// is not the one the convention lands on.
//
// The convention is pluginTile's, and it is open to every plugin equally: a
// namespace's own `overview` capability takes the tile, and failing that the
// first capability that can be previewed at all. `overview` is the word the
// shared vocabulary already uses for exactly this — `dashboard`, `stats` and
// `summary` all normalise to it (pkg/sdk/sdktest) — so an author who wants to
// choose their tile chooses it by naming a capability, not by hoping this map
// learns about them.
//
// What is left here is the one built-in that disagrees with the convention,
// and an entry owes a reason.
//
// kv is that entry. `kv list` would be the intuitive glance and is declared
// first, but it needs the store's passphrase, so on a machine with a store it
// would spend every refresh cycle rendering the same error. `kv status` needs
// nothing — it reads the file's metadata, never its contents — and answers
// the question you actually have at a glance: is the store there, and can
// this shell open it. It is deliberately not spelled `kv.overview`: an
// overview of a secret store reads as a summary *of the secrets*, and the
// whole point of this one is that it never looks at them.
var preferredTile = map[string]string{
	"kv": "kv.status",
}

// autoTiles builds one tile per plugin that has something to show, so nothing
// worth glancing at is invisible from the landing screen.
func autoTiles(reg *registry.Registry) []tile {
	rank := map[string]int{}
	for i, name := range pluginOrder {
		rank[name] = i
	}
	plugins := reg.Plugins() // already sorted by name
	sort.SliceStable(plugins, func(i, j int) bool {
		ri, oki := rank[plugins[i].Name]
		rj, okj := rank[plugins[j].Name]
		if oki != okj {
			return oki // ranked plugins lead; the rest keep alphabetical order
		}
		if oki {
			return ri < rj
		}
		return false
	})
	tiles := make([]tile, 0, len(plugins))
	for _, p := range plugins {
		if t, ok := pluginTile(reg, p); ok {
			tiles = append(tiles, t)
		}
	}
	return tiles
}

// pluginTile picks how one plugin shows itself, and reports whether it can
// show anything at all.
//
// Three rules, in order: what preferredTile pins, the plugin's own
// `<namespace>.overview`, then the first capability that can be previewed.
//
// The middle rule is the one a third-party author can reach. Before it, a
// plugin's tile was whichever previewable capability happened to be declared
// first — so a plugin with a debug dump at the top of its list showed the
// debug dump, and the only way to say otherwise was to be a built-in and get
// named in a map inside rta. `overview` costs no new API to opt into and
// means, in the vocabulary sdktest already enforces, exactly what the tile is
// for.
//
// Every rule goes through previewable, including the pinned one: an override
// is a choice between tiles, never a way to put a capability on a refresh
// timer that said it did not want to be there.
//
// A dashboard is a place you glance at, so a tile has to answer a question
// nobody had to ask first. cert needs a hostname and http needs a URL: there
// is no useful default, and a tile with no live data to show is either the
// same error every five seconds or a static menu — both of which cost a
// screenful of attention to say nothing. Those plugins stay one keystroke
// away in the search bar instead, which is where you go once you do have a
// hostname in mind.
func pluginTile(reg *registry.Registry, p plugin.Plugin) (tile, bool) {
	if id, ok := preferredTile[p.Name]; ok {
		if c, ok := reg.Capability(id); ok && previewable(c) {
			return tile{cap: c}, true
		}
	}
	// The namespace's own overview. Not any capability ending in the word:
	// `net.hosts.overview` would be an overview of the hosts file, which is a
	// section of the plugin rather than the plugin.
	if c, ok := reg.Capability(p.Name + ".overview"); ok && previewable(c) {
		return tile{cap: c}, true
	}
	for _, c := range p.Capabilities {
		if previewable(c) {
			return tile{cap: c}, true
		}
	}
	return tile{}, false
}

// TileFor reports which capability the dashboard would show for a plugin, and
// whether it would show one at all.
//
// Exported for `rta plugin dev`, which exists to tell an author what rta
// believes about their plugin rather than what their source reads like. Which
// capability lands on the landing screen is exactly that shape of fact: it is
// a consequence of safety classes, defaults, NoPreview and declaration order,
// none of which is visible from any one place in the source.
func TileFor(reg *registry.Registry, p plugin.Plugin) (string, bool) {
	t, ok := pluginTile(reg, p)
	if !ok {
		return "", false
	}
	return t.cap.ID, true
}

// previewable reports whether the dashboard may run a capability on its own:
// on load, then again every few seconds, with nobody watching.
//
// Read because a timer must not mutate anything. No required input without a
// default, because there is no one to ask and the tile would render the same
// "missing input" error forever. Not NoPreview, because that is the
// capability saying that running it has a cost the dashboard has no business
// paying unprompted — see plugin.Capability.
func previewable(c plugin.Capability) bool {
	return c.Safety == plugin.Read && !formNeeded(c) && !c.NoPreview
}

// arrange applies the user's adjustments to the automatic set: drop what
// they hid, lead with what they ordered. Both are matched on capability ID,
// and anything they name that no longer exists is simply ignored.
func arrange(tiles []tile, dash config.Dashboard) []tile {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	kept := make([]tile, 0, len(tiles))
	for _, t := range tiles {
		if !hidden[t.cap.ID] {
			kept = append(kept, t)
		}
	}
	if len(dash.Order) == 0 {
		return kept
	}
	rank := map[string]int{}
	for i, id := range dash.Order {
		rank[id] = i
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ri, oki := rank[kept[i].cap.ID]
		rj, okj := rank[kept[j].cap.ID]
		if oki != okj {
			return oki
		}
		if oki {
			return ri < rj
		}
		return false
	})
	return kept
}

// buildTiles resolves the dashboard: an explicit list when the user stated
// one, otherwise one tile per plugin with their hides and ordering applied.
// The live search tile always leads: it is the front door.
func buildTiles(reg *registry.Registry, dash config.Dashboard) []tile {
	var tiles []tile
	for _, ct := range dash.Tiles {
		c, ok := reg.Capability(ct.ID)
		if !ok {
			continue
		}
		// A tile runs on load and again every few seconds, with no form and
		// no confirmation — the destructive gate lives on the CLI and on the
		// TUI's browse path, and a tile goes through neither. Naming a
		// capability in a config file is asking to *watch* it, and this list
		// took any ID at all, so
		//
		//	{id: kv.rm, with: {key: old-token}}
		//
		// deleted the key on startup and kept deleting it, silently, forever.
		// The automatic dashboard already only ever picks Read capabilities;
		// this is the same rule applied to the path a person can write.
		if c.Safety != plugin.Read {
			continue
		}
		tiles = append(tiles, tile{cap: c, values: ct.With, span: ct.Span})
	}
	if len(tiles) == 0 {
		tiles = arrange(autoTiles(reg), dash)
	}
	for i := range tiles {
		tiles[i].actions = capActions(reg, tiles[i].cap.ID)
	}
	search := tile{cap: plugin.Capability{ID: "search", Summary: "find a capability"}, search: true}
	return append([]tile{search}, tiles...)
}

// formNeeded reports whether a capability has required inputs without defaults.
func formNeeded(c plugin.Capability) bool {
	for _, f := range c.Inputs {
		if f.Required && f.Default == nil {
			return true
		}
	}
	return false
}

// tileMsg carries one refreshed tile's view back into the update loop.
//
// id names the capability the result belongs to, and the consumer matches on
// it rather than on idx. A dashboard refresh is in flight for every tile at
// once and `[`/`]` reorder the grid while it is, so an index taken when the
// run started names a different tile by the time the answer arrives — and the
// result is one capability's output painted under another's title, which is
// the worst possible failure for a screen whose whole job is to be glanced at.
//
// idx survives for the static search tile, which has no capability and is
// never actually re-run.
type tileMsg struct {
	id  string
	idx int
	v   view.View
	err *view.Error
}

// tickMsg schedules the next dashboard refresh. gen names which refresh
// chain armed it — see Model.tickGen.
type tickMsg struct{ gen int }

// tileCmd runs one tile's capability off the update loop. Rendering happens
// at paint time so tiles adapt to the current width for free.
func tileCmd(idx int, t tile, cfg map[string]any) tea.Cmd {
	return func() tea.Msg {
		if t.search || t.cap.Run == nil {
			// Static tiles keep their content.
			return tileMsg{id: t.cap.ID, idx: idx, v: t.view}
		}
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		v, err := t.cap.Run(ctx, plugin.NewRequest(
			plugin.Resolve(t.cap, t.values, cfg), false, false).WithSurface(plugin.SurfaceTUI))
		if err != nil {
			return tileMsg{id: t.cap.ID, idx: idx, err: view.AsError(err, t.cap.ID+".failed")}
		}
		return tileMsg{id: t.cap.ID, idx: idx, v: v}
	}
}

// refreshTiles fires every capability tile at once; static tiles keep their
// content. gen is this refresh chain's identity, stamped onto the tick it
// arms so a later chain can tell an earlier one's firing apart from its own.
func refreshTiles(tiles []tile, gen int, pluginCfg func(string) map[string]any) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(tiles)+1)
	for i, t := range tiles {
		if t.search {
			continue
		}
		var cfg map[string]any
		if words := t.cap.Words(); pluginCfg != nil && len(words) > 0 {
			cfg = pluginCfg(words[0])
		}
		cmds = append(cmds, tileCmd(i, t, cfg))
	}
	cmds = append(cmds, tea.Tick(tileRefreshInterval, func(time.Time) tea.Msg { return tickMsg{gen: gen} }))
	return tea.Batch(cmds...)
}

// grid describes the capability-tile geometry below the full-width search
// bar — one source of truth for painting and mouse hit-testing.
type grid struct {
	cols, tileW int
}

// minColWidth is the narrowest a tile column gets before its summaries stop
// being readable. It is derived from where the dashboard already stood: two
// columns from 80 cells up, which is 39 cells and a gap.
const minColWidth = 40

// maxCols caps the automatic grid. Past four the tiles are narrow enough that
// the eye stops scanning columns and starts hunting, and a dashboard you hunt
// through is a dashboard you stop opening.
const maxCols = 4

func (m Model) grid() grid {
	// The dashboard was two columns at every width, so a 200-cell terminal
	// drew two 100-cell tiles of a six-line summary — half the screen spent
	// on whitespace, with the tiles that did not fit pushed below the fold.
	cols := max(min(m.width/minColWidth, maxCols), 1)
	if n := m.dash.Columns; n > 0 {
		cols = max(n, 1)
	}
	// A configured column count still has to fit: honouring columns: 4 on a
	// 60-cell terminal draws four 14-cell tiles, which is not what anybody
	// meant by it.
	cols = max(min(cols, m.width/minColWidth), 1)
	// One gap cell between columns; hit-testing divides by tileW+1.
	return grid{cols: cols, tileW: (m.width - (cols - 1)) / cols}
}

// span is how many columns tile i occupies at the current grid width: what
// the capability declared it needs, what the config overrode it to, clamped
// to a grid that may be narrower than either.
func (m Model) span(i int) int {
	g := m.grid()
	if i <= 0 || i >= len(m.tiles) {
		return 1
	}
	t := m.tiles[i]
	if t.span > 0 {
		return max(min(t.span, g.cols), 1)
	}
	if t.cap.MinWidth <= 0 {
		return 1
	}
	// Each extra column brings its own width plus the gap cell before it.
	n := 1
	for n < g.cols && n*g.tileW+(n-1) < t.cap.MinWidth {
		n++
	}
	return n
}

// tileRows groups the capability tiles into the rows they are drawn on: a
// wide tile takes a row to itself, the rest fill the grid.
//
// This is the one place that decides which tile sits on which row. It used
// to be `i/cols` arithmetic repeated across rendering, hit-testing, scroll
// paging and selection movement — four copies of one rule, correct only for
// as long as every row held exactly cols tiles, and all four of which had to
// be found and changed together the moment one did not.
func (m Model) tileRows() [][]int {
	g := m.grid()
	var rows [][]int
	var cur []int
	used := 0
	flush := func() {
		if len(cur) > 0 {
			rows = append(rows, cur)
			cur, used = nil, 0
		}
	}
	for i := 1; i < len(m.tiles); i++ {
		w := m.span(i)
		if used+w > g.cols {
			flush()
		}
		cur = append(cur, i)
		used += w
		if used >= g.cols {
			flush()
		}
	}
	flush()
	return rows
}

// rowSpan is how many columns a row's tiles claim between them.
func (m Model) rowSpan(cells []int) int {
	n := 0
	for _, i := range cells {
		n += m.span(i)
	}
	return n
}

// tileWidth is how wide one tile is drawn: its span in grid columns, plus
// whatever a short row has left over.
//
// The leftover matters. A wide tile forces the row before it to close early,
// so with nine tiles in two columns the eighth used to be drawn at half
// width with dead space beside it and nothing to explain the hole. A row that
// does not fill the grid shares out what is left, so a short row reads as a
// deliberate arrangement instead of a rendering accident.
func (m Model) tileWidth(i int) int {
	g := m.grid()
	cells := m.rowOf(i)
	if len(cells) == 0 {
		return g.tileW
	}
	spare := 0
	if left := g.cols - m.rowSpan(cells); left > 0 {
		spare = left / len(cells)
	}
	widths := make([]int, len(cells))
	used := len(cells) - 1 // the gap cell between tiles
	for k, idx := range cells {
		sp := m.span(idx) + spare
		widths[k] = sp*g.tileW + (sp - 1)
		used += widths[k]
	}
	// The last tile absorbs whatever integer division left behind, so a row
	// is exactly the screen wide and its right edge lines up with every
	// other row's. Computing each width independently left a cell or two
	// unaccounted for at some terminal sizes, which reads as a wobble down
	// the right-hand side that nothing on screen explains.
	widths[len(cells)-1] += m.width - used
	for k, idx := range cells {
		if idx == i {
			return max(widths[k], 1)
		}
	}
	return g.tileW
}

// rowOf returns the row tile i sits on.
func (m Model) rowOf(i int) []int {
	for _, cells := range m.tileRows() {
		if slices.Contains(cells, i) {
			return cells
		}
	}
	return nil
}

// rowAndCol locates a tile in the row layout.
func (m Model) rowAndCol(idx int) (int, int) {
	for r, row := range m.tileRows() {
		for c, i := range row {
			if i == idx {
				return r, c
			}
		}
	}
	return -1, -1
}

// rowHeights returns each tile row's height, in row order: the tallest
// natural content among that row's tiles, clamped to [tileMinHeight,
// tileHeight]. Tiles within a row always share the row's height, the same
// invariant TestTilesOnSameRowAreUniform checks — only the height itself now
// varies row to row instead of being one constant for the whole grid.
func (m Model) rowHeights() []int {
	rows := m.tileRows()
	heights := make([]int, 0, len(rows))
	for _, row := range rows {
		h := tileMinHeight
		for _, i := range row {
			h = max(h, tileRowHeight(m.tiles[i], m.tileWidth(i)))
		}
		heights = append(heights, h)
	}
	return heights
}

// visibleRowCount is how many rows starting at from fit within avail lines,
// given each row's own height — the shared arithmetic tileAt, dashRowsVisible
// and dashboardView all need to agree on, now that a row's height is no
// longer one constant they could each compute independently.
func visibleRowCount(heights []int, from, avail int) int {
	count, used := 0, 0
	for i := from; i < len(heights); i++ {
		used += heights[i]
		if used > avail {
			break
		}
		count++
	}
	return max(1, count)
}

// tileAt maps a terminal cell to a tile index, -1 for none. Row 0 is the
// header, then the full-width search bar, then tile rows — each its own
// height — offset by the scroll position.
func (m Model) tileAt(x, y int) int {
	if y < 1 {
		return -1
	}
	if y < 1+searchTileHeight {
		return 0
	}
	rows := m.tileRows()
	heights := m.rowHeights()
	yy := y - 1 - searchTileHeight
	row := -1
	for i := m.scroll; i < len(heights); i++ {
		if yy < heights[i] {
			row = i - m.scroll
			break
		}
		yy -= heights[i]
	}
	if row < 0 || row >= m.dashRowsVisible() {
		return -1 // footer area below the last visible tile row
	}
	// Walk the row by drawn width. Dividing x by one column width only ever
	// worked while every tile was one column wide, and the special case for
	// a single full-width tile was that assumption's one exception rather
	// than a rule — a two-column tile beside a one-column one landed every
	// click in the row on the wrong tile.
	cells := rows[row+m.scroll]
	for _, i := range cells {
		w := m.tileWidth(i)
		if x < w {
			return i
		}
		x -= w + 1 // the gap cell between tiles belongs to neither
		if x < 0 {
			return -1
		}
	}
	return -1
}

// dashRowsVisible reports how many tile rows fit under header + search bar,
// starting from the current scroll position.
func (m Model) dashRowsVisible() int {
	if m.height <= 0 {
		return 1 << 30 // size unknown yet: render everything
	}
	avail := m.height - 1 - lipgloss.Height(m.dashFooter()) - searchTileHeight
	return visibleRowCount(m.rowHeights(), m.scroll, avail)
}

// dashRows is the total number of capability-tile rows at the current width.
func (m Model) dashRows() int { return len(m.tileRows()) }

// clampScroll keeps the selected tile visible and the offset in bounds. The
// search bar never scrolls; capability tiles window selection-driven.
func (m *Model) clampScroll() {
	visible := m.dashRowsVisible()
	m.scroll = min(m.scroll, max(0, m.dashRows()-visible))
	if m.selected == 0 {
		return
	}
	selRow, _ := m.rowAndCol(m.selected)
	if selRow < 0 {
		return
	}
	if selRow < m.scroll {
		m.scroll = selRow
	}
	if selRow >= m.scroll+visible {
		m.scroll = selRow - visible + 1
	}
}

// moveSelection shifts the selected tile by (dx, dy): the search bar sits
// above the grid, capability tiles move on it.
func (m *Model) moveSelection(dx, dy int) {
	defer m.clampScroll()
	if m.selected == 0 {
		if dy > 0 || dx > 0 {
			m.selected = min(1, len(m.tiles)-1)
		}
		m.goalCol = 0
		return
	}
	// Left and right walk the tiles in order, which is what they look like
	// they do. Up and down move between rows and keep the column, which is
	// only the same thing while every row is the same length — and a
	// full-width tile makes one of them shorter.
	if dx != 0 {
		if next := m.selected + dx; next >= 1 && next < len(m.tiles) {
			m.selected = next
		}
		// Moving sideways is choosing a column, so it sets the one vertical
		// movement will aim for.
		_, m.goalCol = m.rowAndCol(m.selected)
		return
	}
	if dy == 0 {
		return
	}
	rows := m.tileRows()
	row, col := m.rowAndCol(m.selected)
	if row < 0 {
		return
	}
	// Aim for the column the reader last chose, not the one they happen to
	// be in. Rows are not all the same length — a wide tile closes the row
	// before it early — so reading the column back off the current position
	// let a short row swallow it: from the right-hand column, four presses
	// down and four back up landed on the left-hand column, having passed
	// through a one-tile row on the way. Every text editor solves this the
	// same way and for the same reason.
	if m.goalCol > col {
		col = m.goalCol
	}
	target := row + dy
	if target < 0 {
		m.selected = 0 // up from the top row lands on search
		return
	}
	if target >= len(rows) {
		return
	}
	m.goalCol = col
	m.selected = rows[target][min(col, len(rows[target])-1)]
}

// tileContentLines renders one tile's body at width, full length — the
// natural content driving both how tall its row needs to be (tileRowHeight)
// and what renderTile then clamps to whatever height that row settled on.
func tileContentLines(t tile, width int) []string {
	inner := width - 4
	body := ""
	switch {
	case t.err != nil:
		var buf bytes.Buffer
		_ = cli.RenderError(&buf, t.err, cli.Options{Format: cli.Pretty, Width: inner})
		body = buf.String()
	case t.view != nil:
		var buf bytes.Buffer
		// Fill: a tile is drawn at the grid's width whether its content wants
		// it or not, so the slack belongs to the content rather than to the
		// space beside it.
		if err := cli.Render(&buf, t.view, cli.Options{Format: cli.Pretty, Width: inner, Fill: true}); err == nil {
			body = buf.String()
		}
	default:
		body = theme.Subtle.Render("loading…")
	}
	return strings.Split(strings.TrimRight(body, "\n"), "\n")
}

// tileRowHeight is how tall a row needs to be to show t in full, clamped to
// [tileMinHeight, tileHeight] — the two ends of the range that already
// existed (a fully-shown tile never grew past tileHeight; nothing had a
// floor before because every row was the same fixed height).
func tileRowHeight(t tile, width int) int {
	h := len(tileContentLines(t, width)) + 2 // + top/bottom border
	return min(max(h, tileMinHeight), tileHeight)
}

// renderTile draws one preview panel at the given height — its row's height,
// shared by every tile in that row so neighbors still align exactly the way
// they did under the old fixed tileHeight. The panel primitive guarantees
// exact width × height cells, so overflowing content can never make one tile
// taller than its row.
func renderTile(t tile, width, height int, selected bool) string {
	lines := tileContentLines(t, width)
	preview := height - 2
	if len(lines) > preview {
		lines = lines[:max(preview-1, 0)]
		lines = append(lines, theme.Subtle.Render("… enter for details"))
	}
	return panel(panelHead{Title: t.cap.ID}, strings.Join(lines, "\n"), width, height, selected)
}

// searchResults filters the registry live: prefix matches on the ID lead,
// then substring matches on ID or summary. Every match is returned — the
// window is a rendering decision, made once, in renderSearchTile.
func (m Model) searchResults() []plugin.Capability {
	q := strings.ToLower(strings.TrimSpace(m.query))
	if q == "" {
		return nil
	}
	var prefix, rest []plugin.Capability
	// Straight from the registry rather than from the browse list: the two
	// answer the same question and only one of them has section headers in it.
	for _, c := range m.reg.Capabilities() {
		switch {
		case strings.HasPrefix(strings.ToLower(c.ID), q):
			prefix = append(prefix, c)
		case strings.Contains(strings.ToLower(c.ID+" "+c.Summary), q):
			rest = append(rest, c)
		}
	}
	return append(prefix, rest...)
}

// searchWindow returns the visible slice of matches and where it starts,
// keeping the selection inside it. The offset is derived rather than stored,
// so it cannot drift out of step with a selection the keys just moved.
func (m Model) searchWindow(results []plugin.Capability) ([]plugin.Capability, int) {
	if len(results) <= searchMatches {
		return results, 0
	}
	sel := min(m.searchSel, len(results)-1)
	// The window follows the selection by the shortest move that keeps it
	// visible, which is one line per keypress once you are past the third.
	first := max(0, sel-searchMatches+1)
	return results[first : first+searchMatches], first
}

// renderSearchTile draws the full-width live search bar: a query line and a
// window over the matches, updating on every keystroke.
func (m Model) renderSearchTile(width int, selected bool) string {
	prompt := theme.AccentTxt.Render("❯ ")
	switch {
	case m.searchEditing:
		prompt += m.query + theme.AccentTxt.Render("▌")
	case m.query != "":
		prompt += m.query
	default:
		prompt += theme.Subtle.Render(m.searchInfo)
	}

	lines := []string{prompt}
	results := m.searchResults()
	window, first := m.searchWindow(results)
	inner := width - 4 // what panel() leaves for a body line
	for i, c := range window {
		marker, id := "  ", theme.Subtle.Render(c.ID)
		if first+i == min(m.searchSel, len(results)-1) {
			marker, id = theme.AccentTxt.Render("❯ "), theme.Key.Render(c.ID)
		}
		line := marker + id + theme.Subtle.Render("  "+c.Summary)
		// A caret on the first and last visible line says the list continues
		// in that direction — the same language the tile grid uses. It sits
		// against the right edge, where a scrollbar would be, so it reads as
		// a scroll position rather than as part of the summary.
		more := " "
		switch {
		case i == 0 && first > 0:
			more = theme.Subtle.Render("↑")
		case i == len(window)-1 && first+len(window) < len(results):
			more = theme.Subtle.Render("↓")
		}
		// What it is allowed to do rides in the same right-hand column the
		// catalogue uses, so the answer is in the same place on both screens.
		if inner > 0 {
			line = rightAlign(line, permissionLabel(c)+" "+more, inner)
		}
		lines = append(lines, line)
	}
	if m.query != "" && len(results) == 0 {
		lines = append(lines, theme.Subtle.Render("  no matches"))
	}

	right := "press /"
	if m.searchEditing {
		right = "↑↓ pick · enter run · esc clear"
	}
	// The count is the honest part: it says three of eleven, so a match that
	// is not on screen is a known thing rather than a missing one.
	if n := len(results); n > searchMatches {
		right = fmt.Sprintf("%d/%d · ", min(m.searchSel, n-1)+1, n) + right
	}
	return panel(panelHead{Title: "⌕ search", Right: right},
		strings.Join(lines, "\n"), width, searchTileHeight, selected)
}

// dashFooter builds the dashboard hint bar: the selected tile's own actions
// lead, then navigation, all from the one vocabulary in keys.go.
//
// It is a method rather than an expression inside dashboardView because the
// row budget has to know how tall it came out — a footer that wraps to two
// lines and a grid that still reserves one is how a tile row ends up drawn
// under the terminal's last line.
func (m Model) dashFooter() string {
	items := []hintItem{}
	if m.selected > 0 && m.selected < len(m.tiles) {
		t := m.tiles[m.selected]
		for _, a := range t.actions {
			if a.key == "enter" {
				continue // enter opens the tile itself
			}
			items = append(items, action(a.key, a.label))
		}
		if hint, ok := copyHint(t.cap.ID, t.view); ok {
			items = append(items, hint)
		}
	}
	items = append(items,
		item(bindSelect), labelled(bindOpen, "details"),
		item(bindMove), item(bindHide), item(bindPlugin), item(bindTheme),
		item(bindBrowse), item(bindSearch), item(bindQuit),
	)
	return fitHintBar(m.width, footerMaxLines, items...)
}

// dashboardView lays the search bar and uniform preview tiles out.
func (m Model) dashboardView() string {
	header := theme.Title.Render(" rta") + theme.Subtle.Render("  dashboard")
	footer := m.dashFooter()
	if m.flash != "" {
		footer += theme.GoodText.Render("  ✓ " + m.flash)
	}
	if len(m.tiles) == 0 {
		return header + "\n\n" + theme.Subtle.Render("  no tiles available") + "\n" + footer
	}

	search := m.renderSearchTile(m.width, m.selected == 0)

	rows := m.tileRows()
	heights := m.rowHeights()
	rendered := make([]string, 0, len(rows))
	for r, cells := range rows {
		parts := make([]string, 0, 2*len(cells))
		for _, i := range cells {
			if len(parts) > 0 {
				// A one-cell gap column between tiles — the same gap tileAt
				// divides by.
				parts = append(parts, " ")
			}
			parts = append(parts, renderTile(m.tiles[i], m.tileWidth(i), heights[r], i == m.selected))
		}
		rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Top, parts...))
	}

	// Window the rows to the screen; the selection-driven scroll offset is
	// maintained by clampScroll. Markers show there is more above/below.
	first := min(m.scroll, max(0, len(rendered)-1))
	last := min(first+m.dashRowsVisible(), len(rendered))
	if len(rendered) == 0 {
		return header + "\n" + search + "\n" + footer
	}
	switch {
	case first > 0 && last < len(rendered):
		header += theme.Subtle.Render("  ↕ more")
	case first > 0:
		header += theme.Subtle.Render("  ↑ more")
	case last < len(rendered):
		header += theme.Subtle.Render("  ↓ more")
	}
	return header + "\n" + search + "\n" + strings.Join(rendered[first:last], "\n") + "\n" + footer
}

// tileIndexFor locates the tile a refresh result belongs to, or -1.
//
// By capability, not by position. Every tile refreshes concurrently and the
// grid can be reordered with `[`/`]` or a tile hidden with `H` while results
// are in flight, so the index a run started with is not the index its answer
// comes back to. -1 for a tile that is no longer on the dashboard: the result
// is simply dropped, which is right — nothing is asking for it any more.
func (m Model) tileIndexFor(msg tileMsg) int {
	if msg.id != "" {
		for i, t := range m.tiles {
			if t.cap.ID == msg.id {
				return i
			}
		}
		return -1
	}
	// The static search tile carries no capability.
	if msg.idx >= 0 && msg.idx < len(m.tiles) {
		return msg.idx
	}
	return -1
}
