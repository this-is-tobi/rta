package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
	// err is set instead of values/conn when the environment names this
	// capability but resolving it failed — a `secrets:` reference against a
	// store nothing has unlocked is the ordinary way, since bindCmd runs off
	// the update loop where no passphrase can be asked for. A capability
	// this failed for used to be dropped from the map entirely, which made
	// it indistinguishable from one the environment never mentioned: absent
	// either way, and "absent" is read everywhere else in this file as
	// "run against the base configuration" — so a broken `pg` binding
	// silently ran every pg tile, and any interactive pg call made while
	// the binding was active, against localhost instead of refusing, with
	// the header badge still reading the environment's name throughout.
	err *view.Error
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
	p, ok := cfg.Profiles[config.RefName(name)]
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
// A failure is recorded, not dropped — see envBind.err — because an absent
// entry already means something else (the environment says nothing about
// this plugin), and the two must not read the same: reaching a caller as
// "no profile" ran a call or a tile against the base configuration while
// the header kept naming the environment that was supposed to be in force.
// This runs while painting a switch, not while running a command, so the
// failure surfaces where a caller reaching for this capability's binding
// asks for it — a tile through refreshTiles, an interactive run through
// resolveProfile — each in the place somebody can act on it.
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
		// By the name half: a pinned tile binds a reference, `staging` or
		// `staging/analytics`, and Lookup below is what resolves the
		// instance. A switch is always a bare name, so this is the same
		// line for the environment.
		p, ok := cfg.Profiles[config.RefName(name)]
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
				// Same reasoning as Fill's own failure below: the environment
				// names this capability, so a coordinate that fails to
				// resolve — an instance the environment refers to that is
				// not declared, the ordinary way — is recorded rather than
				// left indistinguishable from a plugin this environment
				// never mentioned.
				out[c.ID] = envBind{err: verr}
				continue
			}
			filled, verr := profile.Fill(ctx, name, conn, c, nil, os.LookupEnv, read)
			if verr != nil {
				// Recorded rather than dropped — see envBind.err — so a
				// caller reading the cache can tell "this environment says
				// nothing about this capability" apart from "it does, and
				// resolving it failed", which an absent map entry cannot.
				out[c.ID] = envBind{err: verr}
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
// name, and the values, or "" and nil when it is silent about that plugin —
// and a refusal, never silently absorbed, when the environment names this
// capability but resolving it failed (envBind.err).
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
func (m Model) profileFor(c plugin.Capability) (string, map[string]any, config.Connection, *view.Error) {
	b, ok := m.bound[c.ID]
	if !ok {
		return "", nil, config.Connection{}, nil
	}
	if b.err != nil {
		return "", nil, config.Connection{}, b.err
	}
	return m.active, b.values, b.conn, nil
}

// pinBind is one pinned profile's binding: what m.bound is for the
// switched-on environment, kept per profile a tile names, under the same
// stamp rule and the same off-the-loop bind. A tile pinned to prod is about
// prod for as long as it is on screen, whatever is switched on, so its
// connection cannot come from the environment cache — and it cannot be
// resolved on every refresh either, because resolving is where a `secrets:`
// reference unlocks the store, and the dashboard refreshes every five
// seconds.
type pinBind struct {
	stamp string
	// ready says the bind has landed, or that err says why it never will.
	ready bool
	// err is a profile that cannot be bound at all — not in the file, or
	// the file unreadable — kept here so every tile pinned to it says so
	// in the tile, rather than "loading…" forever.
	err   *view.Error
	bound map[string]envBind
}

// syncPins brings the pinned bindings in line with the tiles on screen and
// returns the binds to run. Called from the refresh tick beside syncActive,
// for the same reasons: an edit to a pinned profile in another terminal has
// to reach the tile, and this is what runs while nothing is happening.
func (m *Model) syncPins() tea.Cmd {
	start := m.notePins()
	cmds := make([]tea.Cmd, 0, len(start))
	for _, ref := range start {
		cmds = append(cmds, bindCmd(m.reg, ref, m.pins[ref].stamp))
	}
	return tea.Batch(cmds...)
}

// notePins records, for every profile a tile is pinned to, the stamp it
// stands at, and forgets one no tile names any more. It returns the
// references whose bind has to start: named by a tile and not yet noted
// under the stamp the profile now has. Split from syncPins because New
// cannot return a command — it notes, and Init binds what it noted.
func (m *Model) notePins() []string {
	var start []string
	named := map[string]bool{}
	for i, t := range m.tiles {
		if t.profile == "" {
			continue
		}
		named[t.profile] = true
		stamp, verr := pinStamp(t.profile)
		if pin, ok := m.pins[t.profile]; ok && pin.stamp == stamp {
			continue
		}
		if m.pins == nil {
			m.pins = map[string]pinBind{}
		}
		pin := pinBind{stamp: stamp}
		if verr != nil {
			pin.ready, pin.err = true, verr
		} else {
			start = append(start, t.profile)
		}
		m.pins[t.profile] = pin
		// The colour rides on the stamp: it is part of the profile's text,
		// so an edit that changes it is an edit that changes the stamp.
		m.tiles[i].color = profileColor(config.RefName(t.profile))
	}
	for ref := range m.pins {
		if !named[ref] {
			delete(m.pins, ref)
		}
	}
	return start
}

// pinStamp is environmentStamp for a pinned profile, with the two states
// that stamp folds into a string — absent, unreadable — handed back as the
// error the tile should show, since a pinned profile that is not there is
// a fact about the person's config and not a transient.
func pinStamp(ref string) (string, *view.Error) {
	cfg, err := config.LoadFile()
	if err != nil {
		return "unreadable:" + err.Error(), view.AsError(err, "core.profile.config")
	}
	p, ok := cfg.Profiles[config.RefName(ref)]
	if !ok {
		return ref + ":absent", view.Errorf("core.profile.unknown",
			"no profile named %q", config.RefName(ref)).
			WithHint("`rta profile list` shows the ones configured")
	}
	return ref + ":" + profile.Stamp(p) + ":" + kv.StoreStamp(), nil
}

// takeDown adds the way out to a pin's error: the profile it names is the
// whole profile's problem, but the command that takes this tile down is
// about this tile, and only here is the capability known.
func takeDown(verr *view.Error, t tile) *view.Error {
	out := *verr
	out.Hint = strings.TrimSuffix(out.Hint, ".") + "; `rta dashboard rm " + t.cap.ID +
		" --profile " + t.profile + "` takes this tile down"
	return &out
}

// connFor is what a tile runs against: the switched-on environment's
// contribution for a tile that follows it (profileFor), and the pinned
// profile's own binding for one that names it.
//
// A pinned profile that says nothing about the tile's plugin is an error
// on the tile, not a silent run against the base configuration — the same
// rule envBind.err states for the environment, for the same reason: the
// title would name prod while the numbers came from localhost.
func (m Model) connFor(t tile) tileConn {
	if t.profile == "" {
		name, filled, conn, verr := m.profileFor(t.cap)
		return tileConn{name: name, filled: filled, conn: conn, err: verr}
	}
	pin, ok := m.pins[t.profile]
	if !ok || !pin.ready {
		return tileConn{pending: true}
	}
	if pin.err != nil {
		return tileConn{err: takeDown(pin.err, t)}
	}
	b, ok := pin.bound[t.cap.ID]
	if !ok {
		ns := plugin.Namespace(t.cap.ID)
		return tileConn{err: view.Errorf("tui.tile.profile",
			"%s says nothing about %s", t.profile, ns).
			WithHint("`rta profile set " + config.RefName(t.profile) + " --plugin " + ns +
				" --set …` gives it a " + ns + " connection")}
	}
	if b.err != nil {
		return tileConn{err: b.err}
	}
	return tileConn{name: t.profile, filled: b.values, conn: b.conn}
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
		// Only when the bind actually holds this capability, and falling
		// through when it does not rather than answering "no environment".
		//
		// A missing entry is two unrelated things. One is an environment
		// silent about this plugin, which really does mean the base
		// configuration. The other is an environment that names it and whose
		// bind failed — a `secrets:` reference against a store nothing has
		// unlocked is the ordinary way, since bindCmd runs off the update
		// loop where no passphrase can be asked for — and answering that with
		// the base configuration put the base configuration's host in the box
		// under a picker still reading the environment's name, which is the
		// disagreement this function exists to remove. The run was never
		// misdirected (it fails on the same reference), but the screen said
		// otherwise until it did.
		//
		// Falling through re-asks the pure question, which tells them apart:
		// Ambient is silent about the first and answers the second, so what
		// the environment *states* is on screen either way and
		// environmentNotes can name the reference that is about to be needed.
		// covered alone used to be the whole check, back when a failed bind
		// was dropped from the map rather than recorded in it (envBind.err)
		// — now a covered entry can still be the failure this fallback
		// exists to re-ask about, so it takes the same path an absent entry
		// always did.
		if filled, covered := bound[c.ID]; covered && filled.err == nil {
			return on, withoutSecrets(c, filled.values), filled.conn
		}
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
	return m, tea.Batch(bind, refreshTiles(m.tiles, m.tickGen, m.pluginCfg, m.connFor))
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
