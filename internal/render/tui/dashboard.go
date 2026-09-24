package tui

import (
	"context"
	"errors"
	"maps"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A tile is a capability previewed as a dashboard pane. Tiles are pure
// composition — capability ID + values in, View out — the same data-driven
// shape a future a2tea integration would consume. Enter or
// a mouse click opens the full result.
type tile struct {
	cap    plugin.Capability
	values map[string]any
	// profile is the connection this tile is pinned to (config.Tile.Profile),
	// "" for one that follows the switched-on environment. It is part of the
	// tile's identity — see key — and what the panel's title says beside the
	// capability, since two kube.overview tiles are otherwise the same panel
	// twice.
	profile string
	// color is the pinned profile's own colour, "" when it has none: painted
	// on its name in the title the way the header badge paints the
	// switched-on environment's, and on nothing else.
	color string
	// source says where this tile came from, which is what H has to know:
	// an automatic tile is hidden by ID, an entry somebody wrote is removed.
	source tileSource
	// expanded marks one panel of several that one entry became, because
	// the profile it names — or the one switched on — holds several
	// connections for the plugin (expandTiles). H hides such a panel by
	// its key rather than withdrawing the entry, which would take its
	// siblings down with it.
	expanded bool
	// entry is the key of the entry an expanded panel came from —
	// `cnpg.status@ohmlab`, or the bare `cnpg.status` of a tile following
	// the switch — and "" for a panel that is its own entry; entryKey is
	// the one to read. It is what `order:` can place and what a move
	// therefore moves: the file holds the entry, not the panels the
	// switched-on environment happened to expand it into, so an order
	// written in panel keys named `db.status@prod` and `db.status@prod/
	// analytics` while prod was on, and ranked nothing once it was off —
	// the tile a person had put first fell to the end of the screen the
	// moment they switched, and its two panels could be split by moving
	// one past a stranger.
	entry string
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
	// the dashboard's buttons (add/edit/done on the note tile).
	actions []capAction
	// lastFired is when this tile was last dispatched, for a capability
	// that declared its own Refresh pace; zero until the first run. From
	// the dispatch rather than the answer, so a slow answer does not
	// stretch the interval it was waiting out.
	lastFired time.Time
}

// tileSource is where a tile came from: picked for its plugin by the
// automatic dashboard, stated in `tiles:`, or joined through `add:`.
type tileSource int

const (
	tileAuto tileSource = iota
	tileStated
	tileAdded
)

func (s tileSource) String() string {
	switch s {
	case tileStated:
		return "stated"
	case tileAdded:
		return "added"
	}
	return "automatic"
}

// key names this tile the way `order:` and `rta dashboard rm` do: the
// capability, or capability@profile once pinned. Two tiles of one
// capability against two connections are two keys, and moving or removing
// one leaves the other where it is.
func (t tile) key() string { return config.TileKey(t.cap.ID, t.profile) }

// entryKey names the entry this tile stands for: its own key, or the key
// of the entry it is one expanded panel of. `order:` ranks by it, and a
// move moves every panel that shares it.
func (t tile) entryKey() string {
	if t.entry != "" {
		return t.entry
	}
	return t.key()
}

// runValues is what an asked-for run of this tile starts from: its inputs,
// plus the pinned profile under the form's own picker key. Enter on a tile
// about prod has to open prod, and resolveProfile reads the picker exactly
// as it would from a form — an explicit pick beats whatever is switched on
// — so the tile states its pick the way a form would rather than through
// a second channel the run path would have to learn.
func (t tile) runValues() map[string]any {
	if t.profile == "" {
		return t.values
	}
	out := make(map[string]any, len(t.values)+1)
	maps.Copy(out, t.values)
	out[profileInput] = t.profile
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
	// tileMinHeight is the floor a row shrinks to when every tile in it is
	// short: small enough to reclaim real space from a one-line KeyValue or
	// "nothing active" message, tall enough that a panel still reads as a
	// panel next to a taller row rather than a sliver. Tiles within one row
	// always share that row's height — this only ever varies row to row.
	tileMinHeight = 6
	// tileClipHeight is the least a row is drawn at when it is the one row
	// the screen admits without the room for it: the two borders and the
	// line that says there is more. Anything shorter is a box with nothing
	// in it, on a terminal too short for its own footer.
	tileClipHeight = 3
	// searchMatches is how many live results the search bar shows at once.
	// The list itself is not cut to this: it scrolls, because a query that
	// matches eleven capabilities should not silently become three.
	searchMatches = 3
	// searchTileHeight: borders + query line + searchMatches result lines.
	searchTileHeight = searchMatches + 3
)

// pluginOrder is the shipped arrangement: what you glance at most, first.
//
// agent sits beside grant deliberately: grant is the standing policy and
// agent is what happened under it, and a parked call waiting for an answer
// has a clock on it — a dashboard is where somebody notices in
// time rather than after the request has expired.
// Plugins not named here follow, alphabetically — a plugin installed later
// lands on the dashboard without anyone editing this list.
var pluginOrder = []string{"note", "sys", "net", "kv", "grant", "agent"}

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

// NoTileReason says why a plugin has no dashboard tile.
//
// One function because there are three reasons and two callers, and until now
// each caller stated one reason for all of them — in opposite directions. The
// plugin inventory said every tile-less plugin "needs to be told what to look
// at", which is true of cert and http and false of the dozen that decline to
// run unasked; `rta plugin dev`'s report said "nothing here can run unasked",
// which is the same sentence with the same flaw pointed the other way. Both were
// written while looking at the plugin that motivated them.
//
// The distinction is worth keeping precise because the two have opposite
// remedies: a capability that needs input can be given a default and become a
// tile, while one that declined has to be told to run unasked — a decision
// about consent, not about arguments. Telling an operator the wrong one sends
// them to fix something that is not there.
func NoTileReason(p plugin.Plugin) string {
	var read, declined, needsInput int
	for _, c := range p.Capabilities {
		if c.Safety != plugin.Read {
			continue
		}
		read++
		if c.NoPreview {
			declined++
		}
		if formNeeded(c) {
			needsInput++
		}
	}
	switch {
	case read == 0:
		return "nothing here only reads"
	case declined == read:
		return "nothing here runs unasked"
	case needsInput == read:
		return "needs to be told what to look at"
	default:
		return "what it reads either needs input or declines to run unasked"
	}
}

// Unasked says why the dashboard would not run a capability on its own, or
// nothing when it would: the reasons NoTileReason tallies per plugin, one
// capability at a time. Exported for `rta explain`, whose card is per
// capability and had no way to say either — safety, NoPreview and a
// required input's default are three facts on three different lines of a
// declaration, and the tile behaviour they add up to was invisible without
// reading the source.
func Unasked(c plugin.Capability) string {
	switch {
	case c.Safety != plugin.Read:
		return "only a read runs on a timer"
	case c.NoPreview:
		return "it declines to run unasked"
	case formNeeded(c):
		return "it needs to be told what to look at"
	}
	return ""
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

// MissingInputs lists what a tile of c could never fill: a required input
// with no default, no config key a file could fill, nothing under with,
// and — for a pinned tile — nothing a profile could fill either. A tile
// has no form to ask with, so one such input is the same "missing input"
// error on every refresh forever; `rta dashboard add` and `+` in the TUI
// both refuse it here rather than write it.
//
// And a positional credential nothing but the caller can give, required or
// not. codec.jwt's token is not Required, because a pipe supplies it on the
// CLI, and that was the whole of this check: `rta dashboard add codec.jwt`
// wrote a tile answering "no token to read" on every refresh. A tile runs on
// the TUI surface, where no pipe is read; `--set` refuses a credential,
// since it would be written into the config in plaintext; and a profile
// fills only what ProfileFillable allows. The subject of the call, with no
// way in, is as missing as a required input.
func MissingInputs(c plugin.Capability, with map[string]any, pinned bool) []string {
	var missing []string
	for _, f := range c.Inputs {
		if f.Default != nil || f.Config != "" {
			continue
		}
		if _, given := with[f.Name]; given {
			continue
		}
		fillable := plugin.ProfileFillable(c, f)
		switch {
		case f.Required:
			if pinned && fillable {
				continue
			}
		case f.Positional && f.Type.Sensitive() && !fillable:
		default:
			continue
		}
		missing = append(missing, f.Name)
	}
	return missing
}

// Untileable lists the inputs no tile of c can ever be given, whatever it is
// pinned to or states: what MissingInputs still reports with every input
// `--set` accepts given and a profile pinned. `--set` refuses a credential,
// so what is left is a credential nothing but the caller supplies — the
// token codec.jwt decodes, the key codec.jwk reads.
//
// Beside MissingInputs, and exported, because every refusal of a tile words
// itself by it: `rta dashboard add`, explain's dashboard row, and + in the
// TUI, whose refusal kept hinting `--set token=…` for codec.jwt after the
// CLI's had stopped — a command refused in turn, as a credential.
func Untileable(c plugin.Capability) []string {
	settable := map[string]any{}
	for _, f := range c.Inputs {
		if !f.Type.Sensitive() {
			settable[f.Name] = true
		}
	}
	return MissingInputs(c, settable, true)
}

// arrange applies the user's adjustments: drop what they hid, lead with
// what they ordered. A `hidden:` line is a capability ID, which hides an
// automatic tile and every panel it expanded into, or a tile key, which
// hides that one panel wherever it came from — the way H takes one
// connection's panel off an entry that became five, and the one hide a
// stated list is subject to, since a panel of an expansion is not an entry
// the list could be edited to drop. An `add:` entry itself is not hidden
// but withdrawn, by removing the entry, so a stale ID line cannot take
// down a tile somebody wrote in afterwards. Order is matched on the entry
// key, so a pinned tile can be led with on its own and an entry's panels
// move as one; a stated list keeps its own order, as the config documents.
// Anything named that no longer exists is simply ignored.
func arrange(tiles []tile, dash config.Dashboard) []tile {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	kept := make([]tile, 0, len(tiles))
	for _, t := range tiles {
		if (t.source == tileAuto && hidden[t.cap.ID]) || (t.expanded && hidden[t.key()]) {
			continue
		}
		kept = append(kept, t)
	}
	if len(dash.Order) == 0 || len(dash.Tiles) > 0 {
		return kept
	}
	rank := map[string]int{}
	for i, id := range dash.Order {
		rank[id] = i
	}
	sort.SliceStable(kept, func(i, j int) bool {
		ri, oki := rank[kept[i].entryKey()]
		rj, okj := rank[kept[j].entryKey()]
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

// Instances says which connections a profile holds for a plugin, as the
// references a tile can be pinned to: `ohmlab` for the default instance,
// `ohmlab/gitea` for a labeled one. ref is the tile's own profile, "" for a
// tile that follows the switch, and the answer is empty whenever nothing
// would expand: an instance already named, a profile with one connection
// or none, nothing switched on.
type Instances func(ref, ns string) []string

// InstancesOf is the Instances a config and the switched-on profile
// answer. Exported for `rta dashboard list`, which has to show what bare
// `rta` would draw right now, expansions included.
func InstancesOf(cfg config.Config, active string) Instances {
	return func(ref, ns string) []string {
		if config.RefInstance(ref) != "" {
			return nil
		}
		name := ref
		if name == "" {
			name = active
		}
		if name == "" {
			return nil
		}
		p, ok := cfg.Profiles[name]
		if !ok || !p.Trusted() {
			return nil
		}
		labels := p.Instances(ns)
		if len(labels) < 2 {
			return nil
		}
		refs := make([]string, len(labels))
		for i, label := range labels {
			refs[i] = name
			if label != "" {
				refs[i] += "/" + label
			}
		}
		return refs
	}
}

// buildTiles resolves the dashboard without an environment in hand, so
// nothing expands; the model builds through buildTilesWith.
func buildTiles(reg *registry.Registry, dash config.Dashboard) []tile {
	return buildTilesWith(reg, dash, nil)
}

// buildTilesWith resolves the dashboard: an explicit list when the user
// stated one, otherwise one tile per plugin with their hides and ordering
// applied; in both cases the `add:` entries join it, and in both cases a
// tile whose profile holds several connections for its plugin becomes one
// panel per connection. The live search tile always leads: it is the
// front door.
//
// A stated list keeps its own order and is not re-sorted by `order:`, as
// the config documents; the added entries sit after it, in their own
// order, and moveSelected keeps the two apart. A person stating the whole
// dashboard can write a pinned tile straight into `tiles:`, which is why
// the seam is not worth a third ordering rule.
func buildTilesWith(reg *registry.Registry, dash config.Dashboard, instances Instances) []tile {
	tiles := statedTiles(reg, dash.Tiles, tileStated)
	if len(tiles) == 0 {
		tiles = autoTiles(reg)
	}
	tiles = joinTiles(tiles, statedTiles(reg, dash.Add, tileAdded))
	tiles = arrange(expandTiles(tiles, instances), dash)
	for i := range tiles {
		tiles[i].actions = capActions(reg, tiles[i].cap.ID)
	}
	search := tile{cap: plugin.Capability{ID: "search", Summary: "find a capability"}, search: true}
	return append([]tile{search}, tiles...)
}

// joinTiles puts the added entries on the dashboard: after the tiles
// already there, except that an entry whose key is already on screen takes
// that tile's place. The automatic set picks sys.overview for sys on its
// own, and `rta dashboard add sys.overview --span 2` is the one way to say
// how that tile runs — there is no `span:` for a tile nobody wrote — so
// the entry is the tile, in the position the tile had, rather than a twin
// beside it refreshing the same answer twice. H on it withdraws the entry,
// and the automatic tile is back on the next build.
func joinTiles(tiles, added []tile) []tile {
	at := make(map[string]int, len(tiles))
	for i, t := range tiles {
		at[t.key()] = i
	}
	for _, t := range added {
		if i, ok := at[t.key()]; ok {
			tiles[i] = t
			continue
		}
		at[t.key()] = len(tiles)
		tiles = append(tiles, t)
	}
	return tiles
}

// expandTiles turns a tile whose profile names no instance into one panel
// per connection that profile holds for the plugin, each pinned to its own
// reference and named for it.
//
// This is the answer to "one profile, several databases": the ohmlab
// environment holds cnpg/gitea, cnpg/keycloak and three more, and a person
// who adds cnpg.overview wants to glance at all of them, not to be told —
// as the CLI rightly tells a bare `--profile ohmlab` — that the choice is
// theirs. A dashboard is not a choice: showing every one is the glance, no
// wrong pick is possible, and a connection added to the profile next month
// gets its panel on its own, the same rule the automatic set follows for a
// plugin installed next month. One connection stays one panel, so the
// common case reads exactly as it did.
//
// A tile that follows the switch expands into the switched-on profile's
// connections, and is rebuilt on every switch (rebuildTiles): under ohmlab
// it is ohmlab's five, under mirai-prod that environment's own. Each panel
// is pinned for the duration, so it binds through the same per-profile
// cache a pinned tile does, and enter on it opens that connection.
func expandTiles(tiles []tile, instances Instances) []tile {
	if instances == nil {
		return tiles
	}
	colors := map[string]string{}
	out := make([]tile, 0, len(tiles))
	for _, t := range tiles {
		refs := instances(t.profile, plugin.Namespace(t.cap.ID))
		if len(refs) < 2 {
			out = append(out, t)
			continue
		}
		for _, ref := range refs {
			name := config.RefName(ref)
			if _, seen := colors[name]; !seen {
				colors[name] = profileColor(name)
			}
			panel := t
			panel.profile, panel.color, panel.expanded, panel.entry = ref, colors[name], true, t.key()
			out = append(out, panel)
		}
	}
	return out
}

// rebuildTiles resolves the dashboard again — after a switch, since a tile
// following it expands into that environment's own connections, and after
// the arrangement or the inventory changed — carrying each panel's content
// over by key, so the screen does not blank for the tiles still on it.
func (m *Model) rebuildTiles() {
	old := make(map[string]tile, len(m.tiles))
	for _, t := range m.tiles {
		old[t.key()] = t
	}
	fresh := buildTilesWith(m.reg, m.dash, m.instances())
	for i := range fresh {
		if prev, ok := old[fresh[i].key()]; ok {
			fresh[i].view, fresh[i].err, fresh[i].lastFired = prev.view, prev.err, prev.lastFired
		}
	}
	m.tiles = fresh
	m.selected = min(max(m.selected, 0), len(m.tiles)-1)
	m.clampScroll()
}

// instances is InstancesOf for this session: the config as it stands and
// the environment switched on.
func (m Model) instances() Instances {
	cfg, err := config.LoadFile()
	if err != nil {
		return nil
	}
	return InstancesOf(cfg, m.active)
}

// statedTiles resolves the entries a person wrote — `tiles:` or `add:` —
// into tiles, dropping what no longer resolves.
func statedTiles(reg *registry.Registry, entries []config.Tile, source tileSource) []tile {
	var tiles []tile
	for _, ct := range entries {
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
		t := tile{cap: c, values: ct.With, profile: ct.Profile, source: source, span: ct.Span}
		if t.profile != "" {
			t.color = profileColor(config.RefName(t.profile))
		}
		tiles = append(tiles, t)
	}
	return tiles
}

// Placement is one tile of the arrangement as `rta dashboard list` reports
// it: what would be on screen, and why it is there — or why it is not.
type Placement struct {
	ID      string
	Profile string
	Source  string
	Refresh time.Duration
	With    map[string]any
	// Hidden marks a panel `hidden:` keeps off the screen, listed anyway:
	// a person asking why a tile is missing is asking this list.
	Hidden bool
	// Expanded marks one panel of several one entry became.
	Expanded bool
}

// Layout is the arrangement bare `rta` would draw for this config and the
// switched-on environment, search tile excluded and hidden panels marked.
// Exported for `rta dashboard list`, which exists so a person can see the
// dashboard — automatic picks, stated tiles, added ones and what each
// expanded into — without opening it, and so a script can check what it
// added is there.
func Layout(reg *registry.Registry, dash config.Dashboard, instances Instances) []Placement {
	hidden := map[string]bool{}
	for _, id := range dash.Hidden {
		hidden[id] = true
	}
	shown := dash
	shown.Hidden = nil
	tiles := buildTilesWith(reg, shown, instances)
	out := make([]Placement, 0, len(tiles))
	for _, t := range tiles[1:] {
		out = append(out, Placement{
			ID: t.cap.ID, Profile: t.profile, Source: t.source.String(),
			Refresh: t.cap.Refresh, With: t.values, Expanded: t.expanded,
			Hidden: (t.source == tileAuto && hidden[t.cap.ID]) || (t.expanded && hidden[t.key()]),
		})
	}
	return out
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
// key names the tile the result belongs to — the capability, and the
// connection it ran against — and the consumer matches on it rather than on
// idx. A dashboard refresh is in flight for every tile at once and `[`/`]`
// reorder the grid while it is, so an index taken when the run started names
// a different tile by the time the answer arrives — and the result is one
// capability's output painted under another's title, which is the worst
// possible failure for a screen whose whole job is to be glanced at. The
// connection is part of the name for the same reason: a switch rebuilds
// the grid while the previous environment's answers are still in flight,
// and matched by capability alone, prod's numbers landed under whichever
// panel of that capability came first — the one now about staging.
//
// idx survives for the static search tile, which has no capability and is
// never actually re-run.
type tileMsg struct {
	key string
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
// The TUI pays this per call — 54 ms median against a
// real cluster. Tiles are one per plugin by default, so a profile covering pg
// costs one; a hand-configured second tile of the same plugin costs a second
// forward, which works (they get different local ports) and is the rare case.
// It is torn down when the tile finishes, so nothing is held between refreshes.
func tileCmd(idx int, t tile, cfg map[string]any, profileName string,
	filled map[string]any, conn config.Connection) tea.Cmd {
	key := t.key()
	return func() tea.Msg {
		if t.search || t.cap.Run == nil {
			// Static tiles keep their content.
			return tileMsg{key: key, idx: idx, v: t.view}
		}
		ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
		defer cancel()
		// Checked on the context rather than on the error, and before it:
		// a handler that returns ctx.Err() verbatim (builtin/net/trace.go
		// does) and a forward that died mid-open both reach the tile in
		// somebody else's words — "<cap>.failed  context deadline exceeded"
		// — naming neither the deadline that fired nor the fact that
		// opening the tile on its own screen has none. Hoisted above both
		// error branches so the forward's timeout and the handler's get the
		// same sentence.
		deadlineHit := func() *view.Error {
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return nil
			}
			return view.Errorf("tui.refresh.timeout",
				"%s did not answer within %s", t.cap.ID, refreshTimeout).
				WithHint("enter opens it on its own screen, where a run is not on the dashboard's clock")
		}
		dialled, closeTunnel, verr := profile.Dial(ctx, profileName, conn, t.cap, t.values)
		defer closeTunnel()
		if verr != nil {
			if timed := deadlineHit(); timed != nil {
				return tileMsg{key: key, idx: idx, err: timed}
			}
			// Reported, never fallen back from. A tile is where a fallback
			// would be least visible: nobody typed a command to go and look
			// at, so the number on screen would simply be somebody else's.
			return tileMsg{key: key, idx: idx, err: verr}
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
			if timed := deadlineHit(); timed != nil {
				return tileMsg{key: key, idx: idx, err: timed}
			}
			return tileMsg{key: key, idx: idx, err: view.AsError(err, t.cap.ID+".failed")}
		}
		return tileMsg{key: key, idx: idx, v: v}
	}
}

// due reports whether this tile should run at now.
//
// A capability that declared no Refresh runs on every tick, exactly as every
// tile did before the field existed. One that declared a pace waits it out:
// the dashboard's tick keeps its five-second rhythm for the tiles that want
// it, and this one is simply skipped until its own interval has passed —
// no second timer, nothing to arm or cancel, and a tile that was slow to
// answer cannot stack copies of itself the way the every-tick path can.
func (t tile) due(now time.Time) bool {
	if t.search {
		return false
	}
	if t.cap.Refresh <= 0 || t.lastFired.IsZero() {
		return true
	}
	return now.Sub(t.lastFired) >= t.cap.Refresh
}

// resetDue forgets the last run of every tile about the named connection —
// "" for the ones that follow the switched-on environment — so the next
// refreshTiles fires them regardless of pace. For the moments a tile's
// inputs changed under it — the environment switched, and the pg tile is
// now about a different database — where an answer computed for the old
// inputs is wrong however recent it is. Scoped to the connection that
// moved: a tile pinned to prod is about prod whatever was switched, and its
// two-hour pace should not restart because somebody flipped to staging.
func resetDue(tiles []tile, profile string) {
	for i := range tiles {
		if tiles[i].profile == profile {
			tiles[i].lastFired = time.Time{}
		}
	}
}

// tileConn is what a tile runs against: the connection its pinned profile
// or the switched-on environment contributes, the reason it cannot, or
// nothing yet.
type tileConn struct {
	name   string
	filled map[string]any
	conn   config.Connection
	err    *view.Error
	// pending says the tile's pinned profile is still being bound, off the
	// update loop. The tile keeps saying "loading…" rather than showing an
	// error for a state that resolves itself, and is not stamped as fired,
	// so the bind landing runs it at once.
	pending bool
}

// refreshTiles fires every capability tile that is due; static tiles keep
// their content, and so does one still inside the pace its capability
// declared. gen is this refresh chain's identity, stamped onto the tick it
// arms so a later chain can tell an earlier one's firing apart from its own.
//
// connFor is what a tile runs against. For a tile that follows the
// switched-on environment it is what that environment contributes to the
// capability, which is what makes the dashboard answer a question about the
// environment somebody is actually in: switch to proj1-staging and the pg,
// s3 and vault tiles fill from staging, because those are the connections
// that environment names. For a pinned tile it is that profile's own
// binding. nil is the same as nothing switched on and nothing pinned.
func refreshTiles(tiles []tile, gen int, pluginCfg func(string) map[string]any,
	connFor func(tile) tileConn) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(tiles)+1)
	now := time.Now()
	for i := range tiles {
		t := tiles[i]
		if !t.due(now) {
			continue
		}
		var tc tileConn
		if connFor != nil {
			tc = connFor(t)
		}
		if tc.pending {
			continue
		}
		tiles[i].lastFired = now
		var cfg map[string]any
		if words := t.cap.Words(); pluginCfg != nil && len(words) > 0 {
			cfg = pluginCfg(words[0])
		}
		if tc.err != nil {
			// Reported the same way a dial failure already is below, in
			// tileCmd: a tile is where a fallback to the base configuration
			// would be least visible, since nobody typed a command to go
			// and look at, and the number on screen would simply be
			// somebody else's.
			idx, key, verr := i, t.key(), tc.err
			cmds = append(cmds, func() tea.Msg { return tileMsg{key: key, idx: idx, err: verr} })
			continue
		}
		cmds = append(cmds, tileCmd(i, t, cfg, tc.name, tc.filled, tc.conn))
	}
	cmds = append(cmds, tea.Tick(tileRefreshInterval, func(time.Time) tea.Msg { return tickMsg{gen: gen} }))
	return tea.Batch(cmds...)
}

// tileIndexFor locates the tile a refresh result belongs to, or -1.
//
// By key, not by position. Every tile refreshes concurrently and the grid
// can be reordered with `[`/`]`, a tile hidden with `H` or the whole thing
// rebuilt by a switch while results are in flight, so the index a run
// started with is not the index its answer comes back to. -1 for a tile
// that is no longer on the dashboard: the result is simply dropped, which
// is right — nothing is asking for it any more.
func (m Model) tileIndexFor(msg tileMsg) int {
	if msg.key != "" {
		// The position this refresh was actually dispatched from, checked
		// first and exactly: two tiles can watch the same capability
		// against two different `with:` targets (a config listing obj.get
		// twice, once per host), and a config that lists a capability twice
		// is not deduplicated by buildTiles — matching by key alone always
		// finds the first of the two, silently painting one target's result
		// under the other's panel. msg.idx is the index tileCmd actually
		// ran at, so it disambiguates correctly unless the grid was *also*
		// reordered while this one refresh was in flight, which the
		// key-matching fallback below still covers on its own terms.
		if msg.idx >= 0 && msg.idx < len(m.tiles) && m.tiles[msg.idx].key() == msg.key {
			return msg.idx
		}
		for i, t := range m.tiles {
			if t.key() == msg.key {
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
