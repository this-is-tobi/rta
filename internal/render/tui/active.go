package tui

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rule-them-all/builtin/kv"
	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/render/theme"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Which environment this session is in, and what that means for the tiles.
//
// The dashboard is where somebody looks to find out what is going on, so it is
// where being in production has to be visible — and, more than visible, *in
// effect*: a switch that changed which database a command reached but left the
// dashboard showing the old one would be worse than no switch at all, because
// the screen would be quietly answering a question about somewhere else.

// boundMsg carries a finished bind back onto the update loop.
type boundMsg struct {
	name  string
	stamp string
	bound map[string]envBind
}

// envBind is what an environment contributes to one capability: the values it
// fills, and the connection they came from.
//
// Named apart from keys.go's `binding`, which is a keystroke.
//
// The connection is carried rather than re-resolved because the cache is
// consulted on the path that starts a run, and a run against a connection
// naming a cluster has to open a forward — which needs the coordinate. Looking
// it up again there would re-read the config file on the one path this cache
// exists to keep off it, and would read a *different* file if the operator
// edited it in between: the values would describe one connection and the
// forward another, which is the silent-wrong-destination failure profiles
// exist to prevent.
type envBind struct {
	values map[string]any
	conn   config.Connection
}

// syncActive re-reads the switch and returns the command that binds it, or nil
// when nothing changed.
//
// The read is a small file and a stat and happens on the update loop; the
// *bind* can be a second of scrypt and happens off it. That split is the whole
// reason this is two functions: an operator switching to production in the TUI
// must not watch the app freeze, and the badge saying where they are should
// appear with the keypress rather than after the credential it does not need.
//
// Called from the refresh tick as well as from the switch itself, so that
// `rta use` in another terminal and a deadline lapsing while nobody is touching
// the keyboard both reach the screen.
//
// **"Nothing changed" is about the environment, not about its name**, and that
// is the correction. This compared names, so an environment edited in place
// never re-bound: switching to proj1-staging resolved it once, and every host,
// endpoint and region it stated stayed frozen at those values for the rest of
// the session. Every form opened from it was seeded with the old connection and
// — worse, because nothing looked wrong — every command *ran against* it. The
// only way to pick up an edit was to quit and relaunch, which is what an
// operator reported after re-pinning s3 and pg following a rebuild.
//
// Comparing a stamp fixes it for every writer at once. Seven places in this
// package write a profile, `rta profile edit` writes one from another terminal,
// and $EDITOR writes one from nowhere in particular; making each of them
// remember to invalidate a cache is the arrangement that produced the bug, and
// the plugin-config editor remembering while the profile editors did not is
// what it looked like.
func (m *Model) syncActive() tea.Cmd {
	sel := profile.LoadSelection()
	name := sel.Name(time.Now())
	stamp := environmentStamp(name)
	if stamp == m.boundStamp {
		m.activeUntil = nil
		if name != "" {
			m.activeUntil = sel.Until
		}
		return nil
	}
	m.active, m.bound, m.activeUntil, m.boundStamp = name, nil, nil, stamp
	m.activeColor = profileColor(name)
	if name == "" {
		return nil
	}
	m.activeUntil = sel.Until
	return bindCmd(m.reg, name, stamp)
}

// environmentStamp is everything a bind of the named environment depends on:
// which environment it is, what that environment currently states, and the
// identity of the credential store its `secrets:` references are read from.
//
// Nothing switched on stamps as the empty string, which is stable — and is
// also the initial value of Model.boundStamp, so a session that starts with no
// environment does not bind on its first tick.
//
// An unreadable config stamps as its own error rather than as "no environment".
// Answering "" there would read as *switched off* to a screen that is showing
// the badge, and the next repair of the file would then have to fight a cache
// that believes it already knows the answer.
func environmentStamp(name string) string {
	if name == "" {
		return ""
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return "unreadable:" + err.Error()
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return name + ":absent"
	}
	return name + ":" + profile.Stamp(p) + ":" + kv.StoreStamp()
}

// bindCmd resolves what the environment contributes to each capability, keyed
// by capability ID, off the update loop.
//
// Fill rather than Bind, so a `secrets:` reference is actually fetched: a tile
// that cannot authenticate shows an error, and an operator who mapped a
// credential expects the dashboard to use it. This is a place where handlers
// are about to run with somebody watching, which is exactly the line Fill draws.
//
// **The reader is memoised for the length of one bind.** Fill fetches per
// capability, `pg` has six, and every fetch unlocks the store — age's scrypt
// work factor is about a second, so binding one profile would have cost six of
// them for one entry. Memoising by entry name makes it one per distinct entry,
// which is the number of secrets the operator actually mapped.
//
// A capability whose environment says nothing about its plugin gets no entry,
// and runs against the base configuration — the same fall-through the CLI does,
// for the same reason: an environment does not have to contain every plugin.
//
// Failures are dropped rather than surfaced. This runs while painting a switch,
// not while running a command, and the tile that needs the credential reports
// the failure itself, in the place somebody can act on it.
//
// **Fill and not Dial, so no forward is opened here.** This loop covers every
// capability the environment mentions — `pg` alone has six — so dialling would
// raise one port-forward per capability and hold them all for as long as the
// environment stood. A forward is per call by decision and a hole
// in a cluster's network boundary the rest of the time; startRun opens exactly
// one, for the call being made, and closes it when that call ends.
//
// The cost of that split is that this cache is not the whole answer for a
// connection naming a cluster, and startRun knows it: a run adds the dialled
// values on top of whatever was cached here.
func bindCmd(reg *registry.Registry, name, stamp string) tea.Cmd {
	return func() tea.Msg {
		// The bind's own deadline. Reading a Secret out of a cluster is a
		// network call on a path whose failure mode is "the dashboard never
		// finishes switching", and this runs off the update loop where nothing
		// else would ever cancel it.
		ctx, cancel := context.WithTimeout(context.Background(), bindTimeout)
		defer cancel()
		cfg, err := config.LoadFile()
		if err != nil {
			return boundMsg{name: name, stamp: stamp}
		}
		p, ok := cfg.Profiles[name]
		if !ok {
			return boundMsg{name: name, stamp: stamp}
		}
		read := memoRead(kv.Reveal)
		out := map[string]envBind{}
		for _, c := range reg.Capabilities() {
			if !plugin.Profilable(c) || !p.Covers(plugin.Namespace(c.ID)) {
				continue
			}
			conn, verr := profile.Lookup(cfg, c, name, reg)
			if verr != nil {
				continue
			}
			filled, verr := profile.Fill(ctx, name, conn, c, nil, os.LookupEnv, read)
			if verr != nil {
				continue
			}
			out[c.ID] = envBind{values: filled, conn: conn}
		}
		return boundMsg{name: name, stamp: stamp, bound: out}
	}
}

// memoRead answers each entry once. Failures are memoised too: a store that
// cannot be opened will not open on the fourth try either, and retrying costs
// another key derivation to learn the same thing.
func memoRead(read profile.Reader) profile.Reader {
	type answer struct {
		value string
		verr  *view.Error
	}
	seen := map[string]answer{}
	return func(ref string) (string, *view.Error) {
		if a, ok := seen[ref]; ok {
			return a.value, a.verr
		}
		v, verr := read(ref)
		seen[ref] = answer{v, verr}
		return v, verr
	}
}

// profileFor is what the active environment contributes to one capability: its
// name, and the values, or "" and nil when it is silent about that plugin.
//
// Reads the cache directly rather than through currentBind. A tile refresh is
// a display on a five-second timer, and the two answers available when the
// cache is stale are both wrong in a way this cannot fix here: showing the
// previous connection's values, or running the tile *unprofiled* against the
// plugin's base configuration, which is a real connection to a possibly
// different server. The window is bounded instead — by the tick, and by
// closeToOrigin syncing on the way out of every editor — and the paths where
// the answer becomes a command the operator asked for go through currentBind,
// which is exact.
func (m Model) profileFor(c plugin.Capability) (string, map[string]any, config.Connection) {
	b, ok := m.bound[c.ID]
	if !ok {
		return "", nil, config.Connection{}
	}
	return m.active, b.values, b.conn
}

// currentBind is the resolved environment, or nil when what is cached no
// longer describes the environment as it now stands.
//
// The check lives at the point of use as well as in syncActive because a
// command does not wait for a tick. An operator can edit an environment in the
// profiles pane and press enter on a capability a second later, and the five
// seconds between refreshes are five seconds in which the cached values are
// the ones from before the edit.
//
// A caller that finds nothing here falls through to resolving synchronously,
// which is not a degraded answer — it is the same Lookup and Fill the bind
// itself does, and it is the path that already existed for the window between
// a switch and its binding landing. All it costs is the key derivation the
// cache exists to avoid, on the one call that could not use the cache.
//
// **Two questions, and the first one is not about the profile's text.** The
// stamp answers "does this binding still describe what the environment says";
// it cannot answer "is that environment still in force", because a deadline
// lapsing and an `rta use --off` in another terminal both change the selection
// and touch no profile. Selection.Until documents itself as enforced on every
// read, and this is a read: without the first check a production activation
// went on supplying its credentials to every command after it expired, for as
// long as the session stayed off the dashboard — which is where the refresh
// tick that would have noticed lives.
func (m Model) currentBind() map[string]envBind {
	if m.bound == nil || m.active == "" {
		return nil
	}
	if profile.Active() != m.active {
		return nil
	}
	if environmentStamp(m.active) != m.boundStamp {
		return nil
	}
	return m.bound
}

// profileSeed is what the environment called on contributes to c, in the shape
// a form may open showing it.
//
// A form used to open on the base configuration while the run went to the
// environment — the box said prod.db.internal, the picker above it said
// staging, and the call went to staging. A screen that disagrees with what the
// next keypress does is worse than a blank one, because it is believed.
//
// Two differences from profileFor, and both are about this being a screen:
//
//   - Credentials are left out. Seeding a masked input paints the passphrase's
//     length in dots, which is something to know about somebody's credential
//     and nothing the box needs to say — an empty Secret field already means
//     "the environment supplies it", and Fill supplies it when the command runs.
//   - It answers during the bind window. Binding happens off the update loop
//     and a form can be opened inside that second; falling back to Bind here is
//     the same synchronous, pure answer resolveProfile already falls back to,
//     for the same reason.
func (m Model) profileSeed(c plugin.Capability, on string) (string, map[string]any, config.Connection) {
	none := config.Connection{}
	if on == "" || !plugin.Profilable(c) {
		return "", nil, none
	}
	if bound := m.currentBind(); on == m.active && bound != nil {
		filled, covered := bound[c.ID]
		if !covered {
			return "", nil, none
		}
		return on, withoutSecrets(c, filled.values), filled.conn
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return "", nil, none
	}
	// Ambient, not Lookup: an environment that says nothing about this plugin
	// leaves the form on the base configuration rather than showing an error in
	// a place that has nowhere to put one.
	name, conn, verr := profile.Ambient(cfg, c, on, m.reg)
	if verr != nil || name == "" {
		return "", nil, none
	}
	return name, withoutSecrets(c, profile.Bind(name, conn, c, os.LookupEnv)), conn
}

// withoutSecrets drops whatever would land on a masked input.
func withoutSecrets(c plugin.Capability, filled map[string]any) map[string]any {
	out := make(map[string]any, len(filled))
	for k, v := range filled {
		out[k] = v
	}
	for _, f := range c.Inputs {
		if f.Type.Sensitive() {
			delete(out, f.Name)
		}
	}
	return out
}

// backToDashboard leaves a pane, re-reading the switch on the way.
//
// Every return to the dashboard restarts the refresh chain anyway (a tile can
// be stale after any amount of time away), and the switch is exactly the kind
// of thing that changed while somebody was on another screen — including by
// their own hand, one keypress ago.
func (m Model) backToDashboard() (tea.Model, tea.Cmd) {
	m.mode = modeDashboard
	bind := m.syncActive()
	m.tickGen++
	return m, tea.Batch(bind, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.profileFor))
}

// activeBadge is the header's "where am I" line, or "" when nothing is on.
//
// Deliberately loud and deliberately short. The whole value of a switch is that
// somebody can tell at a glance which environment their next keystroke lands
// in, and a badge that has to be read carefully is one that gets read after the
// command rather than before it.
func (m Model) activeBadge() string {
	if m.active == "" {
		return ""
	}
	badge := m.active
	if m.activeUntil != nil {
		if left := time.Until(*m.activeUntil); left > 0 {
			badge += " · " + profile.ShortDuration(left) + " left"
		}
	}
	return badge
}

// paintBadge draws it: the environment's own colour when it has one, and the
// bulleted green this has always been when it does not.
//
// **Green was never wrong and is still the right default.** It says "something
// is switched on", which is all rta knows about an environment nobody marked.
// A colour is the operator saying *which* something, and this is the only
// place in either surface where their colour appears — so it can never be read
// as a status, which is exactly what a repainted palette would have risked.
func (m Model) paintBadge(badge string) string {
	if m.activeColor == "" {
		return theme.GoodText.Render(" ● " + badge)
	}
	return theme.Badge(badge, m.activeColor)
}

// profileColor is the active environment's colour, or "" — read here, off the
// paint path, for the same reason the name is. A colour that is not a colour
// reads as none at all: `rta doctor` is where that gets reported, and a header
// is no place to raise it.
func profileColor(name string) string {
	if name == "" {
		return ""
	}
	cfg, err := config.LoadFile()
	if err != nil {
		return ""
	}
	p, ok := cfg.Profiles[name]
	if !ok || p.BadColor() {
		return ""
	}
	return p.Color
}
