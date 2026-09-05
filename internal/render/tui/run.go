package tui

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"

	"github.com/this-is-tobi/rta/builtin/kv"
	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/recent"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Executing a capability: the seed a run form opens on, the pipeline a
// submitted one goes down — resolve the environment, open the forward, lay
// the layers, run the handler — and the timeout that bounds it.

// runTimeout bounds a single capability execution inside the TUI.
const runTimeout = 30 * time.Second

// formSeed is what a form opens showing: declared defaults, the operator's
// configuration over them, the environment named by on over that, and whatever
// the caller already had on top.
//
// plugin.Resolve rather than a second precedence rule written out here — it
// is the same layered question every surface asks, and two implementations
// of it would disagree on the day one of them was corrected. Seeding rather
// than substituting at submit time is also the better screen: an operator who
// stated a host sees db.internal in the box and can edit it, instead of an
// empty box that silently fills in with something else.
//
// Everything seeded that the caller did not give comes back marked derived —
// see capForm.derived. The caller's own values are theirs whatever the
// environment says, because Resolve gives the caller precedence and this has
// to agree with it; everything else is the form displaying a derivation, and
// a derivation handed back as a caller value outranks the layer it came from
// — which is how a box showing the plugin's default `localhost` beat the
// port-forward a `kube:` connection had just opened. Marked for every layer,
// not only the environment's: the failure is the same whichever layer the
// display came from, and the seed-time environment is not even the one the
// run uses when the picker moves.
// The third return is the environment the seed came from, which runForm needs
// twice over: a connection that tunnels fills the endpoint fields per call and
// is not asked about at all (forwardFilled), and one that references
// credentials answers boxes the seed had to leave empty (environmentNotes).
func (m Model) formSeed(c plugin.Capability, defaults map[string]any, on string) (map[string]any, map[string]bool, formEnv) {
	name, filled, conn := m.profileSeed(c, on)
	seed := plugin.Resolve(c, plugin.Inputs{
		Caller: defaults, Profile: filled, ProfileName: name, Config: m.configFor(c),
	})
	derived := map[string]bool{}
	for input := range seed {
		if _, given := defaults[input]; !given {
			derived[input] = true
		}
	}
	return seed, derived, formEnv{name: name, conn: conn}
}

// formEnv is the environment a form was seeded from: the name profileSeed
// settled on — which is not always the ref the picker shows, since an ambient
// environment resolves through profile.Ambient — and the connection behind it.
type formEnv struct {
	name string
	conn config.Connection
}

// runForm builds a form whose completion runs c.
//
// One constructor rather than the picker and the seed being assembled side by
// side at four call sites, because they have to agree: the fields are seeded
// from an environment and the picker at the top of the same form names it, and
// two of those built from different answers is a form that shows one
// connection and reaches another.
func (m Model) runForm(c plugin.Capability, fs []plugin.Field, defaults, base map[string]any) *capForm {
	on := m.pickedProfile(c, base)
	seed, derived, env := m.formSeed(c, defaults, on)
	shown := fs
	var forwarded []string
	if env.conn.Tunnelled() {
		shown, forwarded = forwardFilled(c, fs, defaults)
	}
	shown = environmentNotes(c, shown, seed, env.name, env.conn)
	cf := newCapForm(c, m.withPicker(c, shown, on, forwarded), seed, true, base)
	cf.derived = derived
	cf.offered = fs
	if b, ok := cf.bindings[profileInput]; ok {
		cf.builtOn = *b
	}
	return cf
}

// reseedOnPickerMove rebuilds the run form on the environment the picker now
// names, the moment it moves.
//
// The picker is at the top because it changes what every other answer means
// — and until this, changing it changed nothing on screen: boxes kept the
// old environment's seeds, hidden endpoint fields stayed hidden under a pick
// that connects directly, and the derived marks were the only thing keeping
// the stale display from becoming the destination. Rebuilding through
// runForm makes the screen the truth again: seeds re-derive from the new
// environment, forwardFilled re-decides what is asked, and a live listing
// fetched under the old environment's credentials dies with the old form
// rather than being offered against the new one. What the person typed is
// exactly what survives — values() already separates their answers from
// displays — and focus lands back on the first field, which is the picker
// they were just on.
func (m Model) reseedOnPickerMove() (tea.Model, tea.Cmd, bool) {
	cf := m.form
	if cf == nil || !cf.final || cf.form.State != huh.StateNormal {
		return m, nil, false
	}
	b, ok := cf.bindings[profileInput]
	if !ok || *b == cf.builtOn {
		return m, nil, false
	}
	values := cf.values()
	m.form = m.runForm(cf.cap, cf.offered, withoutPicker(cf.cap, values), values)
	m.fitForm()
	return m, m.form.form.Init(), true
}

// forwardFilled drops the endpoint-role fields a forward fills per call, and
// returns their names for the picker's help to own.
//
// Dropped rather than shown empty with a hint, which is what this did first:
// an empty box is a question, and three questions the operator must not
// answer outweigh three lines of help saying so. What the boxes displayed is
// not lost — the run's values are provably the same, because an empty
// derived box already contributed nothing and Dial lays host, port and the
// TLS off value at the Profile layer either way. What IS given up is typing
// a host here to bypass the forward this once, deliberately: that path opens
// a forward and then ignores it, and picking the base configuration — or a
// CLI flag — says "connect directly" without the contradiction.
//
// A value the caller already gave keeps its field, because Resolve gives the
// caller precedence and a winning answer must stay on screen to be seen and
// edited.
func forwardFilled(c plugin.Capability, fs []plugin.Field, given map[string]any) ([]plugin.Field, []string) {
	out := make([]plugin.Field, 0, len(fs))
	var dropped []string
	for _, f := range fs {
		if f.Endpoint != plugin.EndpointNone && plugin.ProfileFillable(c, f) {
			if _, ok := given[f.Name]; !ok {
				dropped = append(dropped, f.Name)
				continue
			}
		}
		out = append(out, f)
	}
	return out, dropped
}

// configFor is the operator's stated values for the plugin a capability
// belongs to, by namespace off the ID — which the registry guarantees is the
// plugin that declared it.
func (m Model) configFor(c plugin.Capability) map[string]any {
	if m.pluginCfg == nil {
		return nil
	}
	words := c.Words()
	if len(words) == 0 {
		return nil
	}
	return m.pluginCfg(words[0])
}

// runCmd executes a capability off the update loop. yes reflects an explicit
// in-TUI confirmation for destructive capabilities. The result pane owns the
// whole screen, so detail-capable capabilities are asked for their full view
// unless somebody asked for the other one.
//
// **The forward is opened here and nowhere else in this package.** conn is the
// connection resolveProfile settled on; if it names a cluster, this raises the
// port-forward for the length of this one call and closes it when the call
// ends. It happens inside the returned tea.Msg func because that runs off the
// update loop — an open costs ~54 ms against a real cluster, which is a
// visible stall if it happens on a keypress — and because "as long as the
// call" is the tunnel's whole lifetime rule, which is exactly
// the scope of this function.
func runCmd(ctx context.Context, seq int, c plugin.Capability, values map[string]any, yes bool,
	cfg map[string]any, profileName string, filled map[string]any, conn config.Connection) tea.Cmd {
	// What the form actually collected, before Resolve lays defaults, config
	// and the environment over it.
	collected := values
	return func() tea.Msg {
		dialled, closeTunnel, verr := profile.Dial(ctx, profileName, conn, c)
		defer closeTunnel()
		if verr != nil {
			return resultMsg{cap: c, err: verr, seq: seq}
		}
		if len(dialled) > 0 {
			// Copied rather than written through: filled is the environment
			// bind, which is cached and shared across every run made while
			// that environment stands. Writing this call's endpoint into it
			// would leave a dead forward's address in the cache for the next
			// one, which dials a port nothing is listening on any more.
			merged := make(map[string]any, len(filled)+len(dialled))
			for k, v := range filled {
				merged[k] = v
			}
			for k, v := range dialled {
				merged[k] = v
			}
			filled = merged
		}
		// Resolve rather than "fill defaults only when nothing was given":
		// a caller who supplies one value must not lose the other defaults.
		values = plugin.Resolve(c, plugin.Inputs{
			Caller: values, Profile: filled, ProfileName: profileName, Config: cfg,
		})
		// A default, not an override. Forcing detail on unconditionally made
		// the D toggle on kv.list dead: toggleView set detail=false, this put
		// it back to true one line later, and the handler only ever saw true.
		// The footer checkmark flipped on every press and the pane below it
		// re-rendered the identical detailed page.
		if _, given := values["detail"]; c.Detailed && !given {
			// Copy: tile values are reused by refreshes, which stay compact.
			full := make(map[string]any, len(values)+1)
			for k, v := range values {
				full[k] = v
			}
			full["detail"] = true
			values = full
		}
		start := time.Now()
		v, err := c.Run(ctx, plugin.NewRequest(values, false, yes).WithSurface(plugin.SurfaceTUI))
		elapsed := time.Since(start)
		if err != nil {
			return resultMsg{cap: c, elapsed: elapsed, err: view.AsError(err, c.ID+".failed"), seq: seq}
		}
		// Remembered after it worked, from what the form collected rather than
		// from the resolved request — see internal/recent. Tile refreshes do
		// not come through here, which is what keeps a five-second timer from
		// rewriting the file with the same values forever.
		recent.Record(plugin.SurfaceTUI, c, collected)
		return resultMsg{cap: c, view: v, elapsed: elapsed, seq: seq}
	}
}

// startRun launches a capability and puts the shell in its running state.
//
// The run gets a cancellable context and a sequence number, which is what
// makes "esc" mean something while it is in flight: a traceroute is thirty
// hops of two seconds, and a shell you cannot leave until it finishes is a
// shell that has taken your terminal hostage. Cancelling bumps the sequence,
// so the result that eventually arrives is recognised as belonging to a run
// nobody is waiting for any more, and is dropped instead of painted over
// whatever the user moved on to.
func (m *Model) startRun(c plugin.Capability, values map[string]any, yes bool) tea.Cmd {
	if m.cancelRun != nil {
		m.cancelRun()
	}
	// The connection this run goes to. `profile` is the host's question, not
	// the capability's, so it is taken out of what reaches the handler — but
	// left in what the caller keeps, because that map is what `r` re-runs and
	// `e` reopens, and dropping the answer there would quietly aim the next
	// one at whatever happens to be switched on instead. Every run in the TUI
	// goes through here, which is what makes one extraction enough.
	name, filled, conn, verr := m.resolveProfile(c, values)
	if verr != nil {
		// Surfaced as the run's own result rather than a flash, because it is
		// the answer to what the person just asked for and belongs on the same
		// screen the output would have been on.
		m.runSeq++
		m.mode = modeResult
		m.result = resultMsg{cap: c, err: verr, seq: m.runSeq}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	m.cancelRun = cancel
	m.runSeq++
	m.mode = modeRunning
	return tea.Batch(m.spinner.Tick,
		runCmd(ctx, m.runSeq, c, withoutPicker(c, values), yes, m.configFor(c), name, filled, conn))
}

// resolveProfile takes the picker's answer out of values and binds it.
//
// The precedence is the CLI's: an explicit pick beats whatever is switched on,
// and picking "the base configuration" means exactly that rather than falling
// back to the switch — otherwise the one entry in the list that says "no
// profile" would be the one entry that cannot be chosen.
//
// With nothing picked, the switched-on environment applies where it covers this
// plugin and is silent elsewhere, which is Ambient's rule and the CLI's.
//
// The connection comes back alongside the values because a run against one
// naming a cluster still has to open a forward, and that happens off the update
// loop in runCmd rather than here — this function is called from Update, where
// blocking for the ~54 ms an open costs would be a visible stall on a keypress.
func (m Model) resolveProfile(c plugin.Capability, values map[string]any,
) (string, map[string]any, config.Connection, *view.Error) {
	name, conn, filled, verr := m.pickedConn(c, values)
	if verr != nil || name == "" || filled != nil {
		return name, filled, conn, verr
	}
	filled, verr = m.fillConn(name, conn, c, values)
	if verr != nil {
		return "", nil, config.Connection{}, verr
	}
	return name, filled, conn, nil
}

// pickedConn answers which environment this form's values name and what its
// connection states — everything resolveProfile knows before anything is
// fetched, from the picker, the switch and the config file alone. filled is
// non-nil only when the standing bind already resolved this environment (the
// cache), in which case it is the complete answer and nothing need be
// fetched at all.
//
// Split from the fetching half so a caller can look at the connection before
// resolving its references: completeFromService refuses a coordinate, and a
// refusal that had already read a `kube:` credential would be a Secret
// access in the cluster's audit log caused by a tab that then did nothing —
// plus a stall on the update loop, for nothing.
func (m Model) pickedConn(c plugin.Capability, values map[string]any,
) (string, config.Connection, map[string]any, *view.Error) {
	var none config.Connection
	if !plugin.Profilable(c) {
		return "", none, nil, nil
	}
	name, picked := "", false
	if raw, ok := values[profileInput]; ok {
		if s, isStr := raw.(string); isStr {
			picked = true
			if s != profileNoneLabel {
				name = s
			}
		}
	}
	if !picked {
		// The selection decides, not the cached name. A deadline lapses with
		// nobody touching the keyboard and `rta use --off` happens in another
		// terminal, and m.active is only as fresh as the last refresh tick —
		// which does not run while a form is open, which is exactly where a
		// person spends the minute before pressing enter.
		live := profile.Active()
		// Read once. Two calls each parse the config file and stat the
		// credential store, and — the reason it matters rather than being
		// merely wasteful — the file can change between them: the branch would
		// be entered on the first answer and index a nil map on the second,
		// yielding "not covered", which sends the command to the plugin's base
		// connection while the header says staging. Silently running somewhere
		// else is the failure this whole path exists to prevent.
		bound := m.currentBind()
		switch {
		case live == "":
			return "", none, nil, nil
		case live == m.active && bound != nil:
			// Bound, still in force, and the binding still describes the
			// environment as it now stands: the answer is already resolved,
			// including whether this plugin is one the environment says
			// anything about. No second unlock of the store.
			b, covered := bound[c.ID]
			if !covered {
				return "", none, nil, nil
			}
			return m.active, b.conn, b.values, nil
		default:
			// Switched on but not bound yet — the bind runs off the update loop
			// and a person can start a command inside that window. Resolved here
			// and now rather than treated as "no profile", because the quiet
			// answer is the wrong one: the call would reach the plugin's base
			// connection while the header says staging.
			name = live
		}
	}
	if name == "" {
		return "", none, nil, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", none, nil, view.AsError(err, "core.profile.config")
	}
	var (
		conn config.Connection
		verr *view.Error
	)
	if picked {
		conn, verr = profile.Lookup(cfg, c, name, m.reg)
	} else {
		// Ambient, so an environment that says nothing about this plugin falls
		// through to the base configuration instead of failing — the same
		// difference the CLI draws between a flag and a switch.
		name, conn, verr = profile.Ambient(cfg, c, name, m.reg)
	}
	if verr != nil {
		return "", none, nil, verr
	}
	if name == "" {
		return "", none, nil, nil
	}
	return name, conn, nil, nil
}

// fillConn resolves the connection's references for one call — the half of
// resolveProfile with fetching in it.
//
// Fill, not Bind: a person is waiting, so a `secrets:` reference is
// fetched. The TUI owns the screen, so kv can ask for a passphrase with a
// masked field rather than a terminal prompt — kv.Reveal never prompts, so
// what this gets is the environment's answer or a clear failure.
//
// Fill and not Dial, for bindCmd's reason: this runs on the update loop.
func (m Model) fillConn(name string, conn config.Connection, c plugin.Capability,
	values map[string]any) (map[string]any, *view.Error) {
	ctx, cancel := context.WithTimeout(context.Background(), bindTimeout)
	defer cancel()
	return profile.Fill(ctx, name, conn, c, values, os.LookupEnv, kv.Reveal)
}
