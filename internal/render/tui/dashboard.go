package tui

import (
	"context"
	"errors"
	"maps"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
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
	// switched-on environment's, and on nothing else — the palette is the
	// whole screen's, and a tile pinned to another profile does not move it.
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
	// width is the width, in twelfths of the screen, that the tile's entry, its
	// section or the screen states, nearest first (resolveGrid); 0 when none does,
	// and the tile has what the terminal makes of it (Model.span).
	//
	// widthDemand says which: true when it is the tile's own entry that states it,
	// a demand that beats the capability's MinWidth, and false when it is a
	// default from the section or the screen, which yields to it.
	width       int
	widthDemand bool
	// height is the height of the tile's box in lines, resolved the same way; 0
	// leaves it to what the tile has to say.
	height int
	// refresh is the pace the tile's own entry states, 0 when it states none.
	refresh time.Duration
	// every is the pace the tile runs at once everything that can state one has:
	// its entry, its section, the screen, the capability (resolvePace). 0 on a
	// tile nobody resolved, which runs at its capability's.
	every time.Duration
	// paceFrom says which of the three the pace came from — "tile", "section" or
	// "screen" — and is "" when it is the capability's own, for the list that says
	// where a pace is stated.
	paceFrom string
	// section is the group this tile is drawn in, under a header that folds: the
	// name its entry states, or — for the panels one entry expanded into, when it
	// names none — the plugin and profile they are connections of (expandTiles).
	// "" for a tile in none, which is every tile of a dashboard that does not use
	// them.
	section string
	view    view.View
	err     *view.Error
	// search marks the live search tile: a full-width query bar over the
	// registry, always first.
	search bool
	// actions are one-key shortcuts offered while this tile is selected —
	// the dashboard's buttons (add/edit/done on the note tile).
	actions []capAction
	// lastFired is when this tile was last dispatched; zero until the first
	// run. From the dispatch rather than the answer, so a slow answer does not
	// stretch the interval it was waiting out.
	lastFired time.Time
	// inFlight is whether the run dispatched at lastFired has not answered yet.
	// A tile is not run again while it has not: a run slower than its pace (a
	// bastion that takes ten seconds to open a forward, a database that does not
	// answer until its own timeout) used to be started again at every tick, one
	// more process and one more login on a service that was already slow, and
	// the answers arrived in whatever order they finished.
	inFlight bool
	// storeOpen is whether the store session was open when this tile was last
	// dispatched, for a tile that says something about it (watchesStoreSession).
	storeOpen bool
	// refused is how many runs in a row the service refused this tile's
	// credential (view.Error.CredentialRefused). At credentialRefusals the tile
	// is paused: it is not run again until something asks it to (resume).
	refused int
	// notBefore is when a tile whose capability declares an hour or more between
	// runs may first be run: the moment the TUI opened plus that pace, set once
	// for those tiles (deferSlowLaunches) and zero for every other. Until then the
	// tile is not run and says so, and only r or the clock lifts it.
	notBefore time.Time
	// kept is the answer the tile is drawn from in place of its latest run, set by
	// asReturned for drawing and never stored: nil for a tile drawn as it answered.
	kept *keptAnswer
}

// credentialRefusals is how many answers in a row that refuse a tile's
// credential stop the tile from asking again. One may be a typo that is fixed a
// moment later; two are the credential.
//
// A tile that kept asking was a login every refresh for ever — a pg tile on a
// role that did not exist put a FATAL in the server's log every thirty seconds,
// 2880 a day, and on a database that locks an account that is the way to lock
// it. Only a refusal of the credential counts (a timeout and a refused
// connection are a service that is not there, which the tile has to keep asking
// to say is back), and only in a row.
const credentialRefusals = 2

// paused says the tile has been refused its credential often enough that it is
// no longer run.
func (t tile) paused() bool { return t.refused >= credentialRefusals }

// resume takes the pause off and makes the tile due at the next refresh, which
// is what the person pressing r asks for, and what a connection that changed
// under the tile calls for.
func (t *tile) resume() {
	t.lastFired, t.inFlight, t.refused, t.notBefore = time.Time{}, false, 0, time.Time{}
}

// refusedAfter is the count of refusals in a row once an answer arrives: one
// more when it refused the credential, none when it did anything else.
func refusedAfter(count int, answer *view.Error) int {
	if answer.CredentialRefused() {
		return count + 1
	}
	return 0
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
	// searchMatches is the fewest live results the search box shows at once, and
	// what it shows on a terminal with no room to give it more. The list itself
	// is not cut to this: it scrolls, because a query that matches eleven
	// capabilities should not silently become three. searchSlots says how many
	// it shows on a taller one.
	searchMatches = 3
	// searchMatchesMax is the most the box grows to. Past ten lines it is a page
	// of its own and no longer a window over the dashboard, which is what the
	// tiles under it are there to be seen beside.
	searchMatchesMax = 10
	// searchHeightShare is the part of the terminal's height the box's result
	// lines may be: one in five.
	searchHeightShare = 5
	// searchChrome is what the open box costs beyond its result lines: the two
	// borders and the line the query is typed on.
	searchChrome = 3
	// searchBarHeight is what the search costs the layout while nobody is using
	// it: one line saying it is there. The box with room for results used to be
	// drawn at all times, and on an 80x24 terminal its four empty lines were
	// what kept the tiles that matter off the first screen. It opens to
	// searchTileHeight when the bar takes the keyboard, and closes again when
	// the search is left.
	searchBarHeight = 1
)

// pluginOrder is the shipped arrangement: what you glance at most, first.
//
// sys leads because it is the one tile every machine can answer on the first
// run. agent and grant come straight after it because they are what this
// product is for: grant is the standing policy and agent is what happened
// under it, and a parked call waiting for an answer has a clock on it — a
// dashboard is where somebody notices in time rather than after the request
// has expired. On an 80x24 terminal that is the first screen: the search bar
// costs a row, so sys and agent share the first row and grant the second.
// note is last of the six because its tile is empty until the first note is
// written, and an empty notebook that took the first slot was the first thing
// everybody saw. Plugins not named here follow, alphabetically — a plugin
// installed later lands on the dashboard without anyone editing this list.
var pluginOrder = []string{"sys", "agent", "grant", "net", "kv", "note"}

// leftOff names the capabilities the automatic dashboard declines even though
// they qualify as tiles, each with the reason, worded to follow "not on the
// automatic dashboard — " on `rta explain`'s card.
//
// A plugin whose pick is on this list gets no automatic tile at all, rather
// than the next capability that could be previewed: gen would fall through to
// gen.password, which is the same re-rolled secret as one line. They stay one
// `rta dashboard add` away. The list lives here, beside the picker, because the
// declarations are the plugins' and what a landing screen shows is the host's
// to decide.
var leftOff = map[string]string{
	"gen.overview": "a table of freshly generated secrets is not a status, and it would re-roll on every refresh",
	"fs.tree":      "it prints the directory rta was started in, which says nothing about the machine",
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
// Two rules, in order: the plugin's own `<namespace>.overview`, then the first
// capability that can be previewed.
//
// The first rule is the one every plugin can reach, the built-ins no more and
// no less than a third-party one. Before it, a plugin's tile was whichever
// previewable capability happened to be declared first — so a plugin with a
// debug dump at the top of its list showed the debug dump, and the only way to
// say otherwise was to be a built-in and get named in a map inside rta.
// `overview` costs no new API to opt into and means, in the vocabulary sdktest
// already enforces, exactly what the tile is for: kv names it for a store it
// describes in counts and sizes and opens only from what a call already holds.
//
// Both rules go through previewable: choosing between tiles is never a way to
// put a capability on a refresh timer that said it did not want to be there.
//
// A dashboard is a place you glance at, so a tile has to answer a question
// nobody had to ask first. cert needs a hostname and http needs a URL: there
// is no useful default, and a tile with no live data to show is either the
// same error every five seconds or a static menu — both of which cost a
// screenful of attention to say nothing. Those plugins stay one keystroke
// away in the search bar instead, which is where you go once you do have a
// hostname in mind.
func pluginTile(reg *registry.Registry, p plugin.Plugin) (tile, bool) {
	c, ok := pickCapability(reg, p)
	if !ok {
		return tile{}, false
	}
	if _, left := leftOff[c.ID]; left {
		return tile{}, false
	}
	return tile{cap: c}, true
}

// pickCapability is the two rules of pluginTile, before leftOff has its say.
func pickCapability(reg *registry.Registry, p plugin.Plugin) (plugin.Capability, bool) {
	// The namespace's own overview. Not any capability ending in the word:
	// `net.hosts.overview` would be an overview of the hosts file, which is a
	// section of the plugin rather than the plugin.
	if c, ok := reg.Capability(p.Name + ".overview"); ok && previewable(c) {
		return c, true
	}
	for _, c := range p.Capabilities {
		if previewable(c) {
			return c, true
		}
	}
	return plugin.Capability{}, false
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
//
// A fourth reason is not the plugin's at all: a tile that qualifies and that
// the host leaves off the landing screen (leftOff). It is named first, because
// "nothing here only reads" would be false of gen and fs, which have tiles an
// author could be forgiven for expecting.
func NoTileReason(p plugin.Plugin) string {
	var read, declined, needsInput int
	for _, c := range p.Capabilities {
		if reason, left := leftOff[c.ID]; left && previewable(c) {
			return "the automatic dashboard leaves " + c.ID + " out: " + reason
		}
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
// required input are three facts on three different lines of a declaration,
// and the tile behaviour they add up to was invisible without reading the
// source.
func Unasked(c plugin.Capability) string {
	switch {
	case c.Safety != plugin.Read:
		return "only a read runs on a timer"
	case c.NoPreview:
		return "it declines to run unasked"
	case formNeeded(c):
		return "it needs to be told what to look at"
	}
	return leftOff[c.ID]
}

// previewable reports whether the dashboard may run a capability on its own:
// on load, then again every few seconds, with nobody watching.
//
// Read because a timer must not mutate anything. No required input, because
// there is no one to ask and the tile would render the same "missing input"
// error forever. Not NoPreview, because that is the
// capability saying that running it has a cost the dashboard has no business
// paying unprompted — see plugin.Capability.
func previewable(c plugin.Capability) bool {
	return c.Safety == plugin.Read && !formNeeded(c) && !c.NoPreview
}

// MissingInputs lists what a tile of c could never fill: a required input
// with no config key a file could fill, nothing under with, and — for a
// pinned tile — nothing a profile could fill either. A tile
// has no form to ask with, so one such input is the same "missing input"
// error on every refresh forever; `rta dashboard add` and `+` in the TUI
// both refuse it here rather than write it.
//
// And a Piped input with nothing under with, pinned or not. codec.jwt's
// token is not Required, because a pipe supplies it on the CLI, and Required
// was the whole of this check: `rta dashboard add codec.jwt` wrote a tile
// answering "no token to read" on every refresh. A tile runs on the TUI
// surface, where no pipe is read, and nothing but its with: can give the
// input a value — Validate refuses a Default, a Config key or Local beside
// Piped, so no file and no profile fills one. This inferred the shape once,
// as "a positional credential", which caught codec.jwt and missed
// debug.ansi, whose text is no credential; the declaration says it now.
// Untileable is what still tells a credential, which --set refuses, from
// text it states.
func MissingInputs(c plugin.Capability, with map[string]any, pinned bool) []string {
	var missing []string
	for _, f := range c.Inputs {
		// Not a Default: Validate refuses one beside Required, an empty one
		// included — which fills nothing, and the host refused the call as
		// missing the input all the same (plugin.Missing). Read as a value,
		// it put a tile on the dashboard that answered "missing" on every
		// refresh. Nor beside Piped, which Validate refuses outright.
		if f.Config != "" {
			continue
		}
		if _, given := with[f.Name]; given {
			continue
		}
		switch {
		case f.Piped:
		case f.Required:
			if pinned && plugin.ProfileFillable(c, f) {
				continue
			}
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

// tileNeeds words the host's refusal of a missing input for a tile, as every
// other refusal of a tile is worded. The host names the input the way a form
// does — "fill in the host box" — and a tile has no box: it runs with its
// entry's with:, the profile it runs against and the config, and nobody is
// there to ask. `rta dashboard add` and + refuse such a tile before writing it
// (MissingInputs), so what reaches this is an entry written by hand, or a
// profile that stopped setting an input the tile counted on. The code and the
// sentence stay the host's; only the hint changes, and it never says to write
// a credential into the entry, which `--set` refuses for the same reason.
func tileNeeds(verr *view.Error, c plugin.Capability, values map[string]any) *view.Error {
	missing := plugin.Missing(c, values, plugin.SurfaceTUI)
	if verr.Code != "core.input.missing" || len(missing) == 0 {
		return verr
	}
	f := missing[0]
	out := *verr
	out.Message += ", and a tile has no form to ask with"
	switch {
	case slices.Contains(Untileable(c), f.Name):
		out.Hint = f.Name + " is a credential, which a tile cannot be given — run `" +
			plugin.SurfaceTUI.Call(c.ID) + "` when you have one"
	case f.Type.Sensitive():
		out.Hint = f.Name + " is a credential, which a tile's entry must not hold — `rta explain " +
			c.ID + "` says where else it can come from"
	default:
		out.Hint = "`with: {" + f.Name + ": <value>}` in its dashboard entry states it for every run"
	}
	return &out
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
	// Grouped after the arrangement and not before it: what `order:` and a move
	// decide is where a tile goes, and a section is drawn where its first tile
	// ends up, so ordering the first tile of a section orders the section.
	tiles = groupSections(arrange(expandTiles(tiles, instances), dash))
	for i := range tiles {
		tiles[i].actions = capActions(reg, tiles[i].cap.ID)
		tiles[i].every, tiles[i].paceFrom = resolvePace(tiles[i], dash)
		resolveGrid(&tiles[i], dash)
	}
	search := tile{cap: plugin.Capability{ID: "search", Summary: "find a capability"}, search: true}
	return append([]tile{search}, tiles...)
}

// resolvePace is the pace a tile runs at, and where it was stated: the tile's own
// `refresh:`, else its section's, else the screen's, else what its capability
// gets (plugin.Capability.Pace). The nearest one wins, which is the one rule
// `refresh:` has at every level it can be written.
//
// What a capability declared is a floor under all three (plugin.ClampPace), and
// a value that was cut by it is not reported as having come from the file: the
// tile runs at the capability's pace, and that is the answer to "why does this
// tile not run every ten seconds". A default stated on a section or the screen
// yields to a slower capability without a word, since it was never about that
// tile; a demand on the tile itself that cannot be met is `rta config check`'s
// to say.
func resolvePace(t tile, dash config.Dashboard) (time.Duration, string) {
	want, from := t.refresh, "tile"
	if want == 0 && t.section != "" {
		want, _ = config.ParseRefresh(dash.SectionOf(t.section).Refresh)
		from = "section"
	}
	if want == 0 {
		want, _ = config.ParseRefresh(dash.Refresh)
		from = "screen"
	}
	every, cut := plugin.ClampPace(t.cap, want)
	if want == 0 || cut {
		from = ""
	}
	return every, from
}

// PaceOf is the pace an entry of dash runs at, and where it was stated ("tile",
// "section", "screen", or "" for the capability's own): resolvePace for a caller
// that has the entry and not a built tile, such as the receipt of `dashboard add`.
func PaceOf(c plugin.Capability, entry config.Tile, dash config.Dashboard) (time.Duration, string) {
	t := tile{cap: c, section: strings.TrimSpace(entry.Section)}
	t.refresh, _ = config.ParseRefresh(entry.Refresh)
	return resolvePace(t, dash)
}

// resolveGrid gives a tile the width and the height its section or the screen
// states when its own entry states none: the same cascade as a pace, the nearest
// statement wins, and it is told apart from a demand by widthDemand. A tile's own
// width is kept as it is, and a default width is only ever a default.
func resolveGrid(t *tile, dash config.Dashboard) {
	sec := dash.SectionOf(t.section)
	if t.width == 0 {
		t.width = sec.Width
		if t.width == 0 {
			t.width = dash.Width
		}
	}
	if t.height == 0 {
		t.height = sec.Height
		if t.height == 0 {
			t.height = dash.Height
		}
	}
}

// pace is how often this tile is re-run.
func (t tile) pace() time.Duration {
	if t.every > 0 {
		return t.every
	}
	return t.cap.Pace()
}

// joinTiles puts the added entries on the dashboard: after the tiles
// already there, except that an entry whose key is already on screen takes
// that tile's place. The automatic set picks sys.overview for sys on its
// own, and `rta dashboard add sys.overview --span 2` is the one way to say
// how that tile runs — there is no `span:` for a tile nobody wrote — so
// the entry is the tile, in the position the tile had, rather than a twin
// beside it refreshing the same answer twice. H on it withdraws the entry,
// and the automatic tile is back on the next build.
//
// **It takes the place of the tile it is, and of no other.** The key names a
// capability and a connection, not what the tile is about, and a stated list may
// hold many tiles of one key (a kube.pod.list for each namespace). An entry that
// took the place of whichever of them came last, whatever it asked about, replaced
// the last of the nine with a tenth and left the screen one tile short with
// nothing said. A stated tile is replaced by an entry that asks the same thing;
// the automatic one has no inputs of its own to differ in, so any entry of its
// key replaces it; anything else joins the list.
func joinTiles(tiles, added []tile) []tile {
	for _, t := range added {
		if i := sameTileAt(tiles, t); i >= 0 {
			tiles[i] = t
			continue
		}
		tiles = append(tiles, t)
	}
	return tiles
}

// sameTileAt is where on the screen the tile t already is, or -1: the last
// tile of its key that is the automatic one or asks about the same inputs.
func sameTileAt(tiles []tile, t tile) int {
	at := -1
	for i, o := range tiles {
		if o.key() == t.key() && (o.source == tileAuto || sameInputs(o.values, t.values)) {
			at = i
		}
	}
	return at
}

// sameInputs says two tiles are about the same thing; no inputs and none stated
// are the same.
func sameInputs(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
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
			// An entry that became several panels is a group by being one: the
			// panels of a profile's five databases are the dashboard growing
			// by five at a time, and the section is what lets it fold back. One
			// that stayed one panel is not, which is why this is here and not
			// where the entry is read, and an entry that names its own section
			// keeps it.
			if panel.section == "" {
				panel.section = config.AutoSectionName(plugin.Namespace(t.cap.ID), name)
			}
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
			fresh[i].view, fresh[i].err, fresh[i].lastFired, fresh[i].storeOpen = prev.view, prev.err, prev.lastFired, prev.storeOpen
			fresh[i].inFlight, fresh[i].refused, fresh[i].notBefore = prev.inFlight, prev.refused, prev.notBefore
		}
	}
	m.tiles = fresh
	m.keepSelection()
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
		t := tile{cap: c, values: ct.With, profile: ct.Profile, source: source,
			width: ct.Width, widthDemand: ct.Width > 0, height: ct.Height,
			section: strings.TrimSpace(ct.Section)}
		// A value that cannot be read is the config check's to name; the tile runs
		// at the pace it would have had without the line.
		t.refresh, _ = config.ParseRefresh(ct.Refresh)
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
	// PaceFrom is where the pace was stated — "tile", "section" or "screen" — and
	// "" when it is the capability's own.
	PaceFrom string
	With     map[string]any
	// Hidden marks a panel `hidden:` keeps off the screen, listed anyway:
	// a person asking why a tile is missing is asking this list.
	Hidden bool
	// Expanded marks one panel of several one entry became.
	Expanded bool
	// Section is the group the tile is drawn in, "" for none: the one its entry
	// names, or the automatic one an entry that became several panels is given.
	Section string
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
			Refresh: t.pace(), PaceFrom: t.paceFrom, With: t.values, Expanded: t.expanded, Section: t.section,
			Hidden: (t.source == tileAuto && hidden[t.cap.ID]) || (t.expanded && hidden[t.key()]),
		})
	}
	return out
}

// formNeeded reports whether a capability has a required input — a Piped
// one included, since the dashboard runs on the TUI surface, where there is
// no pipe to fill it. A default beside one is no exception to look for, as
// MissingInputs says.
func formNeeded(c plugin.Capability) bool {
	for _, f := range c.Inputs {
		if requiredHere(f) {
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
func tileCmd(idx int, t tile, cfg statedConfig, profileName string,
	filled map[string]any, conn config.Connection) tea.Cmd {
	key := t.key()
	// Read here, where the update loop builds the command, and not in the
	// closure. The closure runs on a goroutine of its own that nothing waits
	// for once the program has quit, so a tile still in flight when its
	// dashboard closed had read this with nothing ordering the read against
	// anything after it. The tests that lower the deadline write it from the
	// test goroutine, and under -race -shuffle a tile left over from an
	// earlier test's dashboard — one a loaded machine had not yet run when
	// that test quit — was a race reported against whichever test lowered it
	// next. Taken once, it is also the deadline the sentence below names.
	deadline := refreshTimeout
	return func() tea.Msg {
		if t.search || t.cap.Run == nil {
			// Static tiles keep their content.
			return tileMsg{key: key, idx: idx, v: t.view}
		}
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
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
				"%s did not answer within %s", t.cap.ID, deadline).
				WithHint("enter opens it on its own screen, where a run is not on the dashboard's clock")
		}
		// A tile is a call too: the fence a profile states is about what is
		// asked of the connection, whoever wrote the tile, and a refused tile is
		// a visible error in its panel rather than a quiet read of somewhere
		// the profile says it does not reach.
		if verr := profile.Fenced(profileName, conn, t.cap, plugin.Inputs{
			Caller: t.values, Config: cfg.values, ConfigSection: cfg.section,
		}, os.LookupEnv, plugin.SurfaceTUI); verr != nil {
			return tileMsg{key: key, idx: idx, err: verr}
		}
		dialled, via, closeTunnel, verr := profile.Dial(ctx, profileName, conn, t.cap, t.values, plugin.SurfaceTUI)
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
		req := plugin.ResolveRequest(t.cap, plugin.Inputs{
			Caller: t.values, Profile: filled, ProfileName: profileName, Tunnel: via, Config: cfg.values,
			ConfigSection: cfg.section, Fence: profile.FenceOf(conn),
		}, false, false).WithSurface(plugin.SurfaceTUI)
		v, err := t.cap.Run(ctx, req)
		if err != nil {
			if timed := deadlineHit(); timed != nil {
				return tileMsg{key: key, idx: idx, err: timed}
			}
			return tileMsg{key: key, idx: idx,
				err: tileNeeds(view.AsError(err, t.cap.ID+".failed"), t.cap, req.Values())}
		}
		return tileMsg{key: key, idx: idx, v: v}
	}
}

// due reports whether this tile should run at now. A paused one never does:
// something has to resume it first. A deferred one does not either, until the
// clock or r lifts the deferral.
//
// A capability that declared no Refresh runs on every tick, exactly as every
// tile did before the field existed. One that declared a pace waits it out:
// the dashboard's tick keeps its five-second rhythm for the tiles that want
// it, and this one is simply skipped until its own interval has passed —
// no second timer, nothing to arm or cancel, and a tile that was slow to
// answer cannot stack copies of itself the way the every-tick path can.
//
// A tile that says whether the store is unlocked in this TUI is due as soon as
// that stops being what it said (watchesStoreSession): it paces what it costs,
// and what it costs is the decrypt, which a look at the session does not make.
func (t tile) due(now time.Time) bool {
	if t.search || t.paused() || t.deferred(now) {
		return false
	}
	// Before the store-session rule below, which must not start a second run on
	// top of one that has not answered. A run that never answers (its answer
	// dropped with the tile that asked for it) is let go once its own timeout
	// and a tick have passed, so a tile cannot be stuck waiting for nothing.
	if t.inFlight && now.Sub(t.lastFired) < refreshTimeout+tileRefreshInterval {
		return false
	}
	if t.watchesStoreSession() && t.storeOpen != storeUnlocked() {
		return true
	}
	if t.lastFired.IsZero() {
		return true
	}
	// The timer is the tick: a pace no longer than it is every tick, whatever
	// the few microseconds between a dispatch and the tick scheduled after it.
	pace := t.pace()
	return pace <= tileRefreshInterval || now.Sub(t.lastFired) >= pace
}

// resetDue forgets the last run of every tile about the named connection —
// "" for the ones that follow the switched-on environment — so the next
// refreshTiles fires them regardless of pace. For the moments a tile's
// inputs changed under it — the environment switched, and the pg tile is
// now about a different database — where an answer computed for the old
// inputs is wrong however recent it is. Scoped to the connection that
// moved: a tile pinned to prod is about prod whatever was switched, and its
// two-hour pace should not restart because somebody flipped to staging.
//
// A tile that is deferred has no answer to be wrong, and the environment
// settling a moment after launch is the commonest reason to be here: letting it
// through would run every slow tile at once, the burst the deferral is for.
func resetDue(tiles []tile, profile string) {
	now := time.Now()
	for i := range tiles {
		if tiles[i].profile == profile && !tiles[i].deferred(now) {
			tiles[i].resume()
		}
	}
}

// connectionChanged is resetDue for a model, and the other half of it: a tile
// that falls back on its last good answer while its runs fail would, after the
// connection it reads moved, draw the previous environment's numbers under this
// one's name, dimmed and timed and still the wrong destination. What a tile last
// knew about a connection that has changed is not kept.
func (m *Model) connectionChanged(profile string) {
	resetDue(m.tiles, profile)
	raw := make(map[string]returned, len(m.tileRaw))
	for k, r := range m.tileRaw {
		raw[k] = r
	}
	for _, t := range m.tiles {
		if r, ok := raw[rawKey(t)]; ok && t.profile == profile {
			r.good, r.goodAt = nil, time.Time{}
			raw[rawKey(t)] = r
		}
	}
	m.tileRaw = raw
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
func refreshTiles(tiles []tile, gen int, pluginCfg PluginConfig,
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
		tiles[i].lastFired, tiles[i].storeOpen, tiles[i].inFlight = now, storeUnlocked(), true
		cfg := configOf(pluginCfg, t.cap)
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
