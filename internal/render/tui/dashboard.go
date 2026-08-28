package tui

import (
	"context"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
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
//
// **A tile opens the same forward a run does**, and it has to. The cached bind
// carries what an environment *states*; a connection naming a cluster is only
// reachable through a port-forward, and a tile that skipped it ran the handler
// against the plugin's own default host while the badge said the cluster
// profile was on. On a machine with a local PostgreSQL that is real data from
// the wrong database, refreshed every five seconds, with nothing saying so — the
// silent wrong destination this whole feature exists to remove, arriving
// through the one path that did not dial.
//
// The cost is one forward per tunnelled tile per refresh, which is the price
// ADR 0018 §4 already said the TUI would pay per call — 54 ms median against a
// real cluster. Tiles are one per plugin by default, so a profile covering pg
// costs one; a hand-configured second tile of the same plugin costs a second
// forward, which works (they get different local ports) and is the rare case.
// It is torn down when the tile finishes, so nothing is held between refreshes.
func tileCmd(idx int, t tile, cfg map[string]any, profileName string,
	filled map[string]any, conn config.Connection) tea.Cmd {
	return func() tea.Msg {
		if t.search || t.cap.Run == nil {
			// Static tiles keep their content.
			return tileMsg{id: t.cap.ID, idx: idx, v: t.view}
		}
		ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
		defer cancel()
		dialled, closeTunnel, verr := profile.Dial(ctx, profileName, conn, t.cap)
		defer closeTunnel()
		if verr != nil {
			// Reported, never fallen back from. A tile is where a fallback
			// would be least visible: nobody typed a command to go and look
			// at, so the number on screen would simply be somebody else's.
			return tileMsg{id: t.cap.ID, idx: idx, err: verr}
		}
		if len(dialled) > 0 {
			// Copied, not written through: filled is the shared environment
			// bind, and leaving this call's endpoint in it would hand the next
			// refresh a dead forward's address.
			merged := make(map[string]any, len(filled)+len(dialled))
			for k, v := range filled {
				merged[k] = v
			}
			for k, v := range dialled {
				merged[k] = v
			}
			filled = merged
		}
		v, err := t.cap.Run(ctx, plugin.NewRequest(plugin.Resolve(t.cap, plugin.Inputs{
			Caller: t.values, Profile: filled, ProfileName: profileName, Config: cfg,
		}), false, false).WithSurface(plugin.SurfaceTUI))
		if err != nil {
			return tileMsg{id: t.cap.ID, idx: idx, err: view.AsError(err, t.cap.ID+".failed")}
		}
		return tileMsg{id: t.cap.ID, idx: idx, v: v}
	}
}

// refreshTiles fires every capability tile at once; static tiles keep their
// content. gen is this refresh chain's identity, stamped onto the tick it
// arms so a later chain can tell an earlier one's firing apart from its own.
//
// forProfile is what the switched-on environment contributes to a capability.
// It is what makes the dashboard answer a question about the environment
// somebody is actually in: switch to proj1-staging and the pg, s3 and vault
// tiles fill from staging, because those are the connections that environment
// names. nil is the same as nothing switched on.
func refreshTiles(tiles []tile, gen int, pluginCfg func(string) map[string]any,
	forProfile func(plugin.Capability) (string, map[string]any, config.Connection)) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(tiles)+1)
	for i, t := range tiles {
		if t.search {
			continue
		}
		var cfg map[string]any
		if words := t.cap.Words(); pluginCfg != nil && len(words) > 0 {
			cfg = pluginCfg(words[0])
		}
		var (
			name   string
			filled map[string]any
			conn   config.Connection
		)
		if forProfile != nil {
			name, filled, conn = forProfile(t.cap)
		}
		cmds = append(cmds, tileCmd(i, t, cfg, name, filled, conn))
	}
	cmds = append(cmds, tea.Tick(tileRefreshInterval, func(time.Time) tea.Msg { return tickMsg{gen: gen} }))
	return tea.Batch(cmds...)
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
