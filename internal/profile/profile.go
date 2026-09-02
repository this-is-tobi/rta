// Package profile resolves an operator's named environments.
//
// A profile is a named overlay across several pinned plugins' already-declared
// inputs: everywhere "proj1-staging" is, in one name. It is the human's fast
// switch (`rta use proj1-staging`), the unit an agent's permission is granted
// against, and — while one is switched on — the bound on what agents may reach
// at all. Those are deliberately the same object: a permission model people
// find quick to use is one they actually turn on, and the alternative — a
// separate agent-policy block — is a second set of rules that never expires and
// therefore never gets renewed.
//
// The package is split by what a function may do, and the split is
// load-bearing. Bind is pure — config file and process environment, no
// process, no socket, no store — which is what makes it safe on the
// completion surface, resolved on every keystroke, and inside the TUI's form
// seed, which has neither a context nor an error return. Fill is Bind plus
// everything that has to be *done* rather than read — the fetching a
// `secrets:` block asks for, the credentials read out of a cluster, and the
// port-forward a `kube:` coordinate names — so it takes a context, returns a
// teardown, and is called only where a handler is actually about to run with
// somebody waiting: never from completion, and over MCP never before the grant
// gate has approved the call. A hole in a cluster's network boundary must not
// be opened for a call that is about to be refused.
package profile

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/internal/tunnel"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Origin answers where a namespace came from. The same shape
// pluginconf.Origin has, and the same one registry.Registry.Origin and
// mcp.Options.Origin already satisfy.
type Origin func(namespace string) (registry.Origin, bool)

// Installed is what resolving a profile has to know about this machine: where
// a namespace came from, and what its capabilities declare.
//
// *registry.Registry satisfies it and is the only implementation there will
// ever be. It is an interface so that the two halves travel together: they were
// separate for a while, with Lookup taking only Origin, and the cost was
// precise — Check could see that `hsot: typo.internal` reaches nothing and
// Lookup could not, so a misspelled host was reported as invalid by `rta
// profile list` and resolved anyway by every path that runs a command. The call
// then went to the plugin's *base* connection while the operator believed they
// were on staging, which is the exact shape of the failure profiles exist to
// prevent.
//
// A nil Installed resolves nothing and therefore refuses every external
// plugin's profiles, which is the right zero for a gate: a caller who forgets
// to wire it loses profiles rather than losing the check.
type Installed interface {
	Origin(namespace string) (registry.Origin, bool)
	Capabilities() []plugin.Capability
}

// withheld is implemented by an Installed that also knows which artifacts
// discovery found and refused to launch.
//
// Optional, and asserted rather than required, because it exists only to tell
// two causes apart in a sentence: a plugin that is not there, and a plugin
// that is there and has not been approved. An Installed that cannot say gets
// the older, weaker wording rather than a compile error, and the registry —
// which is the usual implementation and legitimately knows nothing about
// trust — stays as it is.
type withheld interface {
	Untrusted(namespace string) bool
}

// Lookup finds the profile called name and confirms it may be used for c.
//
// Every failure is the same shape and, over MCP, the same *text*: an unknown
// profile and a profile belonging to another plugin must not be
// distinguishable by an agent, or the refusal becomes an oracle for the
// operator's connection inventory. The caller decides how much to say — this
// returns a code and a message, and internal/mcp replaces the message with a
// generic one.
func Lookup(cfg config.Config, c plugin.Capability, ref string, inst Installed) (config.Connection, *view.Error) {
	ns := plugin.Namespace(c.ID)
	none := config.Connection{}
	// The reference splits into the profile and, optionally, which instance
	// of this plugin inside it: `staging` or `staging/analytics`. Split
	// first, because everything profile-level below is about the name — the
	// instance only matters once the profile's entries for ns are in hand.
	name, instance := config.SplitRef(ref)
	if !config.ValidRef(ref) {
		return none, view.Errorf("core.profile.invalid",
			"%q is not a valid profile reference", ref).
			WithHint("a name — lowercase letters, digits and dashes, starting with a letter " +
				"or digit — optionally followed by /<instance> to pick one of several " +
				"connections to the same plugin")
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return none, view.Errorf("core.profile.unknown",
			"no profile named %q", name).
			WithHint(configured(cfg, ns))
	}
	// A profile from ./.rta.yaml is refused rather than ignored. Ignoring it
	// would silently run the call against the base connection, which is the
	// one outcome nobody asked for: the operator who wrote the profile thinks
	// they are talking to it.
	if !p.Trusted() {
		return none, view.Errorf("core.profile.untrusted",
			"profiles in a working-directory config file are not honoured").
			WithHint("a profile names somewhere else, so it is only read from the config " +
				"path rta owns — set $RTA_CONFIG to name this file deliberately")
	}
	if unknown := p.UnknownKeys(); len(unknown) > 0 {
		return none, migrationOr(name, p, view.Errorf("core.profile.unknownkey",
			"profile %q has %s, which nothing reads", name, strings.Join(unknown, ", ")).
			WithHint("a profile takes plugins, note and ttl"))
	}
	// Ahead of resolution, deliberately: two entries for one namespace and
	// instance — a stale pin left beside its replacement, most realistically
	// — is not something first-match-by-sort-order can answer for. Silently
	// resolving through whichever one sorts first is exactly how Check
	// reports a profile broken while a call through it quietly succeeds
	// anyway, which the doc comment above states this function must not
	// allow: "Lookup refuses what Check reports." Scoped to the entry this
	// call addresses: a duplicated `pg/analytics` does not break a call for
	// the pg default, though Check still reports it.
	addressed := ns
	if instance != "" {
		addressed = ns + "/" + instance
	}
	if dups := p.DuplicateNamespaces(); slices.Contains(dups, addressed) {
		return none, view.Errorf("core.profile.duplicate",
			"profile %q names %s more than once", name, addressed).
			WithHint("`rta profile show " + name + "` lists every entry — remove the one that no longer applies")
	}
	var (
		key     string
		conn    config.Connection
		covered bool
	)
	switch {
	case instance != "":
		key, conn, covered = p.ForInstance(ns, instance)
		if !covered && p.Covers(ns) {
			// The profile covers the plugin; the label is what missed. Told
			// apart from silence because the fix is different: a mistyped
			// label wants the list, not "configure pg".
			return none, view.Errorf("core.profile.instance",
				"profile %q has no %s instance called %q", name, ns, instance).
				WithHint("it has: " + strings.Join(instanceRefs(p, name, ns), ", "))
		}
	case p.Ambiguous(ns):
		// Several labeled connections and no default. Picking one by sort
		// order would send the call to whichever database's label happens to
		// alphabetize first, so the caller is asked to say which — this is
		// the refusal that makes a labeled entry addressable at all.
		return none, view.Errorf("core.profile.instance.required",
			"profile %q holds several %s connections, so a call must name which one", name, ns).
			WithHint("one of: " + strings.Join(instanceRefs(p, name, ns), ", "))
	default:
		key, conn, covered = p.For(ns)
	}
	if !covered {
		return none, view.Errorf("core.profile.mismatch",
			"profile %q says nothing about %s", name, ns).
			WithHint(configured(cfg, ns))
	}
	// A labeled key whose label does not parse is refused as what it is; it
	// would otherwise surface as the pin check misreading `pg/x_y` as a
	// namespace nothing registered.
	if _, keyInst, _ := config.SplitKey(key); keyInst != "" && !config.ValidInstance(keyInst) {
		return none, view.Errorf("core.profile.instance.invalid",
			"profile %q, under %s: %q is not a valid instance label", name, key, keyInst).
			WithHint("lowercase letters, digits and dashes, starting with a letter")
	}
	if !plugin.Profilable(c) {
		return none, view.Errorf("core.profile.unusable",
			"%s has no input a profile could fill", c.ID).
			WithHint("a profile overlays inputs a plugin offered to configuration; this " +
				"capability offers none")
	}
	if unknown := conn.UnknownKeys(); len(unknown) > 0 {
		return none, view.Errorf("core.profile.unknownkey",
			"profile %q, under %s, has %s, which nothing reads",
			name, key, strings.Join(unknown, ", ")).
			WithHint("a plugin entry takes set, secrets, kube, ssh and tunnelTLS")
	}
	if bad := conn.BadSecretRefs(); len(bad) > 0 {
		return none, view.Errorf("core.profile.secret.scheme",
			"profile %q: %s under %s names no source", name, strings.Join(bad, ", "), key).
			WithHint("write it as `kv:<entry>` — a bare name would be ambiguous the day " +
				"a second source exists, and guessing is not something to do about a credential")
	}
	if p.BadTTL() {
		return none, view.Errorf("core.profile.ttl",
			"profile %q has ttl %q, which is not a duration", name, p.TTL).
			WithHint("write it as `30m`, `2h`, `12h` — or remove it for no deadline")
	}
	// The tunnel parses, checked here and reported by Check, so a typo is
	// found before the call that needs it rather than during. The network
	// questions — does the context exist, does the bastion answer — are left
	// to the open, because asking them here would spawn a kubectl or an ssh
	// per profile on a path people run while something is already broken.
	if verr := checkTunnel(conn); verr != nil {
		return none, view.Errorf("core.profile.tunnel",
			"profile %q, under %s: %s", name, key, verr.Message).WithHint(verr.Hint)
	}
	// A coordinate the plugin cannot be pointed at is refused, not warned
	// about. Without this, the forward is opened, fills nothing, and the call
	// proceeds to the plugin's own default host — real data from the wrong
	// server, under a badge naming the cluster. That is the failure profiles
	// exist to prevent, and it is exactly what a stale plugin binary produces:
	// endpoint roles are declared by the artifact, so a plugin built before
	// they existed declares none. `rta profile show` had this sentence as an
	// advisory "problem" row while every run sailed past it — page-versus-run
	// drift, fifth recorded instance, in the direction that hides the failure.
	//
	// Only when the declaration was actually read. An unregistered namespace
	// falls through to the pin check below, which names the real problem —
	// and refuses just the same, so nothing is loosened by declining to
	// guess here.
	fillable, declRead := tunnellable(config.PluginNamespace(key), inst)
	if conn.Tunnelled() && declRead && !fillable {
		return none, view.Errorf("core.profile.untunnellable",
			"profile %q: %s declares no input a tunnel can fill, so the forward would "+
				"be opened and ignored", name, config.PluginNamespace(key)).
			WithHint("the call would reach the plugin's own destination, not the tunnel. " +
				"`rta profile set " + name + " --plugin " + config.PluginNamespace(key) +
				" --direct` removes the `" + conn.TunnelKey() + ":` line — a plugin that " +
				"reaches its service another way needs no forward, and one that dials but " +
				"predates endpoint roles needs rebuilding instead")
	}
	// The pin, enforced here and not only reported by Check.
	//
	// This was the whole point of pinning and it was briefly advisory: `rta
	// profile list` printed "invalid" for an unpinned profile while the very
	// same profile resolved and connected. A check that appears on a report and
	// not on the path that uses the value is not a check — it is a label. And
	// the thing it labels is the one that matters most here: registration is
	// first-come and $PATH decides the order, so an unpinned profile hands
	// whichever binary answered to "pg" the operator's stated connection.
	if verr := checkPin(key, inst); verr != nil {
		return none, verr
	}
	// And the `set:` block, enforced here for the same reason and not only
	// reported. A key nothing reads is a value the operator believes is in
	// effect: `hsot: staging.db.internal` resolves to nothing, and the call
	// reaches whatever the plugin's own configuration says — production, if
	// that is what the base section holds — while `rta profile show` prints
	// the connection they meant.
	if problems := checkSet(name, key, conn, ns, inst); len(problems) > 0 {
		return none, view.Errorf("core.profile.set",
			"profile %q: %s", name, problems[0].Reason).WithHint(problems[0].Hint)
	}
	// And the `secrets:` block, for the same reason: a mapping that can never
	// take effect is a credential the operator believes is in play and is not.
	if problems := checkSecretRefs(name, key, conn, ns, inst); len(problems) > 0 {
		return none, view.Errorf("core.profile.secrets",
			"profile %q: %s", name, problems[0].Reason).WithHint(problems[0].Hint)
	}
	return conn, nil
}

// migrationOr replaces "these keys read nothing" with the migration
// instruction when the keys are the ones the single-plugin shape used.
//
// The generic message is technically true and useless: somebody whose file
// worked last release wants to be told what it became, not that `plugin` is
// unrecognised.
func migrationOr(name string, p config.Profile, generic *view.Error) *view.Error {
	if !p.Legacy() {
		return generic
	}
	return view.Errorf("core.profile.legacy",
		"profile %q is written in the old single-plugin shape", name).
		WithHint("a profile is an environment now — move `plugin: pg@abcd` and its `set:`/" +
			"`secrets:` under `plugins:\n    pg@abcd:\n      set: …`, and add the rest of " +
			"the environment beside it")
}

// minPinLen matches internal/plugintrust's and internal/mcp's own
// digest-prefix floor: short enough to type, long enough that grinding a
// second artifact to collide with it is not a realistic attack.
const minPinLen = 8

// checkPin confirms a profile's plugin key names the artifact that is actually
// installed.
//
// A nil origin resolves nothing and therefore refuses every external plugin,
// which is the right zero value for a gate: a caller who forgets to wire it
// loses profiles rather than losing the check.
func checkPin(key string, inst Installed) *view.Error {
	ns, _, pin := config.SplitKey(key)
	pinned := pin != ""
	var (
		o     registry.Origin
		known bool
	)
	if inst != nil {
		o, known = inst.Origin(ns)
	}
	switch {
	case !known:
		// Installed and refused is a different problem from not installed, and
		// it is the one an operator hits by rebuilding a plugin: trust is keyed
		// on the digest, so a rebuild drops the approval and the plugin stops
		// registering. Told "not a registered plugin", they go looking for a
		// missing install — and the environment naming it is refused whole, so
		// one rebuilt plugin can take four working ones off the air.
		if w, ok := inst.(withheld); ok && w.Untrusted(ns) {
			return view.Errorf("core.profile.untrustedplugin",
				"profile names %q, which is installed and has not been run", ns).
				WithHint("`rta plugin trust " + ns + "` approves the artifact; rebuilding a " +
					"plugin changes it, so it needs approving again")
		}
		return view.Errorf("core.profile.unknownplugin",
			"profile names %q, which is not a registered plugin", ns).
			WithHint("`rta plugin list` shows what is installed, including anything " +
				"found and not run")
	case !o.External():
		if pinned {
			return view.Errorf("core.profile.pinned",
				"%q is built in and has no artifact to pin", ns).
				WithHint("write it as `" + ns + ":`")
		}
	case !pinned:
		return view.Errorf("core.profile.unpinned",
			"%q is an installed plugin, so a profile entry for it must name the artifact", ns).
			WithHint("write it as `" + ns + "@" + o.Short() + ":`")
	case len(pin) < minPinLen:
		// Below the floor every other digest-prefix match in the codebase
		// shares (internal/plugintrust, internal/mcp's --allow-destructive
		// pin): short enough to be cheap to grind, which turns "survives a
		// rebuild without silently re-trusting a different artifact" —
		// pinning's whole point — back into trusting whatever currently
		// answers to the name. Every writer of a profile entry
		// (profileset.go, the TUI, explain.go) already always emits the
		// full 12-char short digest; this only closes a hand-written or
		// copy-truncated one.
		return view.Errorf("core.profile.shortpin",
			"this profile's pin for %q is too short to trust", ns).
			WithHint("the installed one is `" + ns + "@" + o.Short() + "` — at least " +
				strconv.Itoa(minPinLen) + " hex characters")
	case !strings.HasPrefix(o.Digest, pin):
		return view.Errorf("core.profile.stalepin",
			"this profile's pin does not match the installed %q", ns).
			WithHint("the installed one is `" + ns + "@" + o.Short() + "`")
	}
	return nil
}

// Ambient resolves the switched-on profile for one capability, and is silent
// when it has nothing to say about that plugin.
//
// The difference from Lookup is entirely about how the name arrived. `--profile
// proj1-prod` on a command that pg does not appear in is a mistake worth
// stopping: somebody asked for something that cannot happen. But being switched
// on to proj1-prod and running `rta sys disk` is not a mistake at all — an
// environment does not have to contain every plugin, and refusing there would
// make switching on cost you every command the profile is silent about.
//
// So a profile that says nothing about this namespace yields "", and the call
// runs against the base configuration exactly as it would with nothing switched
// on. A profile that says something *broken* about it still fails, because that
// is a statement, and running somewhere else instead is the wrong answer
// delivered confidently.
func Ambient(cfg config.Config, c plugin.Capability, name string, inst Installed) (string, config.Connection, *view.Error) {
	if name == "" || !plugin.Profilable(c) {
		return "", config.Connection{}, nil
	}
	p, known := cfg.Profiles[name]
	if !known || !p.Covers(plugin.Namespace(c.ID)) {
		return "", config.Connection{}, nil
	}
	conn, verr := Lookup(cfg, c, name, inst)
	if verr != nil {
		return "", config.Connection{}, verr
	}
	return name, conn, nil
}

// originOf and capabilitiesOf read an Installed that may be nil, which is the
// state a caller who forgot to wire one leaves behind. Both answer "nothing is
// installed", so the failure is lost profiles rather than lost checks.
func originOf(inst Installed, ns string) (registry.Origin, bool) {
	if inst == nil {
		return registry.Origin{}, false
	}
	return inst.Origin(ns)
}

func capabilitiesOf(inst Installed) []plugin.Capability {
	if inst == nil {
		return nil
	}
	return inst.Capabilities()
}

// instanceRefs renders a profile's entries for one namespace the way a call
// would name them — `staging` for the default, `staging/analytics` for a
// labeled one — so a hint about instances is in the grammar the fix uses.
// For a person at a terminal; internal/mcp discards hints.
func instanceRefs(p config.Profile, name, ns string) []string {
	labels := p.Instances(ns)
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		if l == "" {
			out = append(out, name)
			continue
		}
		out = append(out, name+"/"+l)
	}
	return out
}

// configured names the profiles that would have worked, for a person at a
// terminal. Never reaches an agent: internal/mcp discards this hint.
func configured(cfg config.Config, ns string) string {
	names := cfg.ProfilesFor(ns)
	if len(names) == 0 {
		return "no profiles are configured for " + ns + " — `rta profile list` shows what is"
	}
	return "configured for " + ns + ": " + strings.Join(names, ", ")
}

// Bind is everything a profile contributes, keyed by INPUT name.
//
// Two sources, in this order:
//
//   - Set, translated from Field.Config keys through c's own declaration. An
//     operator writes the key they would write under plugins:, and this is
//     where it becomes the input name a handler reads.
//   - RTA_PROFILE_<NAME>_<INPUT>, for the inputs a profile may carry a
//     credential for. A config file never holds a secret, so this
//     is the channel — narrower than the namespace-wide RTA_<NS>_<INPUT>,
//     which plugin.Resolve switches off entirely while a profile is active.
//
// The environment wins, because Set comes from a plaintext file and a
// credential does not live there. Nothing here can reach an input
// plugin.ProfileFillable refuses.
func Bind(name string, conn config.Connection, c plugin.Capability, look func(string) (string, bool)) map[string]any {
	out := map[string]any{}
	byConfig := map[string]plugin.Field{}
	for _, f := range c.Inputs {
		if !plugin.ProfileFillable(c, f) {
			continue
		}
		if f.Config != "" {
			byConfig[f.Config] = f
		}
	}
	for key, v := range conn.Set {
		if f, ok := byConfig[key]; ok {
			out[f.Name] = v
		}
	}
	// The environment channel exists for the default instance only. A ref
	// like `staging/analytics` has no RTA_PROFILE_* spelling: every separator
	// an env token can carry is one a legal profile name can also produce
	// ("staging--x" and "staging/x" would both render STAGING__X), so a
	// labeled instance's variable would be forgeable by naming a profile
	// carefully — a repoint by spelling. Labeled instances carry credentials
	// the way multi-database setups already do, through `secrets:`
	// references, where nothing is derived from a string.
	if config.RefInstance(name) != "" {
		return out
	}
	for _, f := range c.Inputs {
		if !plugin.ProfileFillable(c, f) {
			continue
		}
		if v, ok := look(plugin.ProfileEnvVar(name, f.Name)); ok {
			out[f.Name] = v
		}
	}
	return out
}

// Reader fetches one referenced secret. builtin/kv.Reveal has this shape.
//
// Injected rather than imported, which is the point: internal/profile is
// reached from internal/mcp on every gated call and from the CLI's completion
// path on every keystroke, and it has no business knowing that an encrypted
// store exists. The two call sites that can actually unlock one say so
// explicitly, and everything else passes nil and gets no secrets.
type Reader func(ref string) (string, *view.Error)

// Fill is Bind plus the credentials a profile's `secrets:` block references —
// a local store entry for `kv:`, a cluster Secret for `kube:`.
//
// Split from Bind because Bind must stay pure. It runs on the completion
// surface, which resolves on every keystroke, and inside the TUI's form seed,
// which has neither a context nor an error return — neither may unlock a store
// or reach a cluster.
//
// Called only from the paths that actually run a handler with somebody
// waiting: the CLI's runCapability, the TUI's environment bind, and
// internal/mcp's handler **after** grant.Reserve has approved the call. A
// secret must never be fetched for a call that is about to be refused.
//
// A caller-supplied value wins, so nothing is fetched for an input the person
// already typed — the same precedence Resolve applies, applied early enough to
// avoid the work.
//
// **This opens no tunnel, and that separation is load-bearing.** What Fill
// produces describes the environment and is the same answer for every call
// made while that environment stands, which is why the TUI resolves it once at
// the switch and holds it — unlocking an age store costs about a second of
// scrypt and the dashboard refreshes every five. A forward is the opposite: it
// is per call by decision (because a port-forward outlives the pod
// it points at) and holding one for as long as an environment would leave a
// hole in a cluster's network boundary open across a whole session, one per
// capability the environment covers. Dial is that half, and it is called where
// a run actually begins.
func Fill(ctx context.Context, name string, conn config.Connection, c plugin.Capability,
	caller map[string]any, look func(string) (string, bool), read Reader,
) (map[string]any, *view.Error) {
	out := Bind(name, conn, c, look)

	fillable := map[string]plugin.Field{}
	for _, f := range c.Inputs {
		if plugin.ProfileFillable(c, f) {
			fillable[f.Name] = f
		}
	}
	// wanted records the inputs a reference would fill and nothing else
	// already has, so the cluster read below fetches exactly those. Walked
	// here rather than inside kubeSecrets because the precedence rules — a
	// typed value wins, then an env var — are Fill's and apply to every
	// scheme.
	wanted := map[string]bool{}
	for _, ref := range conn.SecretRefs() {
		if _, ok := fillable[ref.Input]; !ok {
			// Refused rather than skipped. An operator who mapped a credential
			// onto an input this capability does not have has written something
			// that will never take effect, and finding that out from an
			// authentication failure three steps later is the experience this
			// whole feature exists to remove.
			return nil, view.Errorf("core.profile.secret.unknown",
				"profile %q maps a secret onto %q, which %s does not offer",
				name, ref.Input, plugin.Namespace(c.ID)).
				WithHint("`rta explain " + c.ID + "` lists the inputs it declares")
		}
		if _, typed := caller[ref.Input]; typed {
			continue
		}
		// An env var for the same input beats the reference: it is the more
		// explicit statement, and it is what lets somebody override one
		// connection's credential for a single shell without editing config.
		if _, fromEnv := out[ref.Input]; fromEnv {
			continue
		}
		switch ref.Scheme {
		case "kube":
			wanted[ref.Input] = true
			continue
		case "kv":
		default:
			return nil, view.Errorf("core.profile.secret.scheme",
				"profile %q resolves %s through %s:, which names no source", name, ref.Input, ref.Scheme)
		}
		if read == nil {
			return nil, view.Errorf("core.profile.secret.unavailable",
				"profile %q needs an entry from the store and this surface cannot open one", name)
		}
		v, verr := read(ref.Ref)
		if verr != nil {
			// Wrapped rather than passed through, because kv's own message is
			// about a store and the person is looking at a connection.
			return nil, view.Errorf("core.profile.secret.failed",
				"profile %q: reading %s for %s: %s", name, ref.Ref, ref.Input, verr.Message).
				WithHint(verr.Hint)
		}
		out[ref.Input] = v
	}
	// The cluster, in the order argued at the top: credentials first, so a
	// refusal costs no forward.
	if len(wanted) > 0 {
		fromCluster, verr := kubeSecrets(ctx, name, conn)
		if verr != nil {
			return nil, verr
		}
		for input, v := range fromCluster {
			// Only the ones nothing more explicit already answered. kubeSecrets
			// reads a whole Secret per call, so it can return an input a typed
			// value or an env var has already won.
			if wanted[input] {
				out[input] = v
			}
		}
	}
	return out, nil
}

// Dial opens the forward this connection's `kube:` coordinate names and
// returns the inputs it fills, together with the teardown that closes it.
//
// **close is never nil, and must be called on every path.** For the
// overwhelmingly common connection — no `kube:` at all — this returns an empty
// map and a no-op, so `defer close()` on the line after the call is
// unconditionally correct, including when verr is non-nil: a failure after the
// forward came up closes it here before returning.
//
// It is deliberately not grant.Reserve's release, which the MCP handler calls
// four lines away. release is a *refund* and must run only when the call
// failed; this is a teardown and must run always. A forward left open is a hole
// in a cluster's network boundary with nobody watching, and the two are named
// apart so that reading one as the other is a compile error rather than a leak.
//
// **Separate from Fill because their lifetimes are different**, not merely
// because it is tidy. Fill's answer describes the environment and is cached for
// as long as one stands; this is per call by decision, because a port-forward
// outlives the pod it points at and a cached tunnel to a rescheduled pod fails
// in a way nobody can read. Measured at 54 ms median to open
// against a real cluster, which is the cost that has to stay worth it.
//
// No `caller` argument, and that is not an omission: plugin.Resolve already
// applies Caller above Profile, so a host somebody typed wins over this
// without anything here checking. What these values do override is the
// plugin's own default and the operator's `set:` — which is right, because a
// `set:` host beside a `kube:` coordinate is two statements about where the
// call goes and the forward is the one that exists.
func Dial(ctx context.Context, name string, conn config.Connection, c plugin.Capability,
) (map[string]any, func(), *view.Error) {
	noop := func() {}
	if !conn.Tunnelled() {
		return nil, noop, nil
	}
	tun, verr := tunnel.Open(ctx, name, target(conn))
	if verr != nil {
		return nil, noop, verr
	}
	return endpointValues(c, tun.Endpoint, conn.TunnelTLS), tun.Close, nil
}

// Problem is one thing wrong with a configured profile.
//
// Reported rather than returned as an error, because `rta doctor` has to be
// able to show every one of them at once — an operator fixing their config
// wants the whole list, not the first line of it.
type Problem struct {
	Name string
	// Plugin is the entry this is about, "" when it is about the profile as a
	// whole. Carried rather than folded into Reason so a screen can group
	// problems under the plugin they belong to without parsing a sentence back
	// apart — and so `rta doctor`'s line and the TUI's row cannot disagree
	// about which plugin an operator has to go and fix.
	Plugin string
	Reason string
	Hint   string
}

func (p Problem) String() string {
	where := "profiles." + p.Name
	if p.Plugin != "" {
		where += "." + p.Plugin
	}
	if p.Hint == "" {
		return fmt.Sprintf("%s: %s", where, p.Reason)
	}
	return fmt.Sprintf("%s: %s (%s)", where, p.Reason, p.Hint)
}

// Check validates every configured profile against the registry.
//
// A profile with any problem is one an operator must fix before it does
// anything: Lookup refuses what Check reports, so an invalid profile is
// unnameable on every surface rather than half-applied on one.
func Check(cfg config.Config, inst Installed) []Problem {
	var problems []Problem
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		add := func(reason, hint string) {
			problems = append(problems, Problem{Name: name, Reason: reason, Hint: hint})
		}
		if !config.ValidName(name) {
			add("not a valid profile name",
				"lowercase letters, digits and dashes, starting with a letter or digit")
			continue
		}
		if !p.Trusted() {
			add("read from a working-directory config file, so it is not honoured",
				"set $RTA_CONFIG to name this file deliberately")
			continue
		}
		// Before anything about the plugins block, deliberately. A misspelled
		// `plguins:` produces both problems at once, and "names no plugin" sends
		// somebody looking for a line that is right there in front of them —
		// while "has plguins, which nothing reads" is the whole answer.
		if unknown := p.UnknownKeys(); len(unknown) > 0 {
			verr := migrationOr(name, p, view.Errorf("x",
				"has %s, which nothing reads", strings.Join(unknown, ", ")).
				WithHint("a profile takes plugins, note and ttl"))
			add(verr.Message, verr.Hint)
			continue
		}
		if len(p.Plugins) == 0 {
			add("configures no plugin", "add a `plugins:` block naming at least one")
			continue
		}
		if p.BadTTL() {
			add("has ttl "+p.TTL+", which is not a duration",
				"write it as `30m`, `2h`, `12h` — or remove it for no deadline")
		}
		if _, taken := originOf(inst, name); taken {
			add("has the same name as a registered plugin",
				"rename it — a profile name and a namespace share a command line")
		}
		// Ahead of the per-key loop below: a namespace named twice is a
		// problem about the profile's shape, not about either entry on its
		// own — the same reason Lookup refuses it before ever asking For to
		// pick one. Reported even when every individual entry would
		// otherwise pass CheckConnection, which a stale-pin duplicate is not
		// guaranteed to trip.
		for _, ns := range p.DuplicateNamespaces() {
			add("names "+ns+" more than once, so which entry governs is ambiguous",
				"`rta profile show "+name+"` lists every entry — remove the one that no longer applies")
		}
		for _, key := range p.PluginKeys() {
			problems = append(problems, CheckConnection(name, key, p.Plugins[key], inst)...)
		}
	}
	return problems
}

// CheckConnection reports what is wrong with one plugin entry, without needing
// the profile it sits in to exist anywhere yet.
//
// Split out of Check for the surface that writes one. A command that builds a
// connection from flags has to refuse a broken one *before* it lands in the
// file — an operator scripting their setup is not watching a report — and the
// only way for that refusal and this report to stay the same refusal is for
// them to be the same code. Every rule this package has ever duplicated
// eventually disagreed with itself; this is the one shape that cannot.
//
// Profile-level rules stay in Check, because they are about the profile: its
// name, whether the file it came from is trusted, its ttl, and whether it
// configures anything at all.
func CheckConnection(name, key string, conn config.Connection, inst Installed) []Problem {
	var problems []Problem
	at := func(reason, hint string) {
		problems = append(problems, Problem{Name: name, Plugin: key, Reason: reason, Hint: hint})
	}
	if unknown := conn.UnknownKeys(); len(unknown) > 0 {
		at("has "+strings.Join(unknown, ", ")+", which nothing reads",
			"a plugin entry takes set, secrets, secrets-from, kube, ssh and tunnelTLS")
		return problems
	}
	// The same refusal Lookup makes for a label that does not parse, in the
	// same words — before the pin check, which would otherwise misread
	// `pg/x_y` as a namespace nothing registered.
	if _, instance, _ := config.SplitKey(key); instance != "" && !config.ValidInstance(instance) {
		at("is written under "+key+", and "+instance+" is not a valid instance label",
			"lowercase letters, digits and dashes, starting with a letter")
		return problems
	}
	if bad := conn.BadSecretRefs(); len(bad) > 0 {
		at(strings.Join(bad, ", ")+" names no source", "write it as `kv:<entry>`")
		return problems
	}
	// The same check Lookup enforces, in the same words. Two copies of
	// this rule is how `rta profile list` came to print "invalid" for
	// a profile that connected perfectly well.
	if verr := checkTunnel(conn); verr != nil {
		at(verr.Message, verr.Hint)
		return problems
	}
	// The same rules Lookup enforces, in the same words, so what this
	// report calls invalid is exactly what refuses to resolve. Two
	// copies of a rule is how `rta profile list` came to print
	// "invalid" for a profile that connected perfectly well — and how
	// `rta profile show` came to print a tunnel "problem" that every
	// run sailed past.
	// Only when there was a declaration to read; otherwise checkPin below
	// says what is actually wrong.
	fillable, declRead := tunnellable(config.PluginNamespace(key), inst)
	if conn.Tunnelled() && declRead && !fillable {
		at(config.PluginNamespace(key)+" declares no input a tunnel can fill, "+
			"so the forward would be opened and ignored",
			"`rta profile set "+name+" --plugin "+config.PluginNamespace(key)+
				" --direct` removes the `"+conn.TunnelKey()+":` line — a plugin that reaches "+
				"its service another way needs no forward, and one that dials but predates "+
				"endpoint roles needs rebuilding instead")
		return problems
	}
	if verr := checkPin(key, inst); verr != nil {
		at(verr.Message, verr.Hint)
		return problems
	}
	problems = append(problems, checkSet(name, key, conn, config.PluginNamespace(key), inst)...)
	problems = append(problems, checkSecretRefs(name, key, conn, config.PluginNamespace(key), inst)...)
	return problems
}

// checkTunnel reports what is wrong with the tunnel a connection states —
// two of them at once, or one that does not parse. One function feeding both
// Check and Lookup, because two copies of a rule is how the page and the run
// learn to disagree; the scheme-specific halves live in internal/tunnel
// beside the resolvers that honour them.
func checkTunnel(conn config.Connection) *view.Error {
	if conn.Kube != "" && conn.SSH != "" {
		return view.Errorf("core.profile.tunnel.twice",
			"states both `kube:` and `ssh:` — a call opens one forward").
			WithHint("keep whichever names where this connection really goes")
	}
	if conn.TunnelTLS && !conn.Tunnelled() {
		return view.Errorf("core.profile.tunnel.tls",
			"states `tunnelTLS: true` with neither `kube:` nor `ssh:` — there is no forward for it to describe").
			WithHint("tunnelTLS: true tells the host the far side of a tunnel speaks TLS on its own; " +
				"remove it, or add the `kube:`/`ssh:` line it belongs beside")
	}
	// Two statements about where this connection's Secrets come from, which is
	// one fact. Refused rather than ordered, the same treatment `kube:` and
	// `ssh:` get one check above: a coordinate already names the namespace its
	// Secrets are read from, so a `secrets-from:` beside it is either the same
	// answer written twice or a second, different one that a reader has no way
	// to rank.
	if conn.Kube != "" && conn.SecretsFrom != "" {
		return view.Errorf("core.profile.secrets.twice",
			"states both `kube:` and `secrets-from:` — a `kube:` coordinate already names "+
				"the namespace its Secrets are read from").
			WithHint("drop `secrets-from:`, or drop `kube:` if this connection reaches its " +
				"service directly and only its credentials live in the cluster")
	}
	if conn.SecretsFrom != "" {
		if verr := tunnel.CheckKubeNamespace(conn.SecretsFrom); verr != nil {
			return verr
		}
	}
	if conn.Kube != "" {
		return tunnel.CheckKube(conn.Kube)
	}
	if conn.SSH != "" {
		return tunnel.CheckSSH(conn.SSH)
	}
	return nil
}

// tunnellable reports whether any capability in the namespace declares an
// input a forward can fill — the condition under which a `kube:` coordinate
// or an `ssh:` target means anything at all. Endpoint roles are a fact about
// the installed artifact, so a rebuilt plugin can gain or lose this without
// the config file changing a character.
// tunnellable answers whether a tunnel can fill anything ns declares, and
// separately whether ns was there to ask.
//
// The second return is the whole point. A namespace with no registered
// capabilities is not a plugin that declares nothing — it is a plugin nobody
// read, and the two are opposite conclusions from the same empty slice.
// Collapsing them produced a refusal asserting what an unread declaration
// says, under a hint offering to delete the operator's `kube:` line: the
// commonest way to get here is a **stale pin**, where the artifact is
// installed and working and the profile names an older digest of it. The
// coordinate is correct, the pin is not, and the advice was to destroy the
// correct half.
func tunnellable(ns string, inst Installed) (fillable, known bool) {
	var mine []plugin.Capability
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) == ns {
			mine = append(mine, c)
		}
	}
	if len(mine) == 0 {
		return false, false
	}
	return plugin.Tunnellable(mine), true
}

// checkSet reports a key of Set that no capability in the namespace reads, a
// value outside a declared Options set, or an endpoint key a coordinate
// shadows.
//
// The first two are the checks internal/pluginconf makes about a plugins:
// section, for the same reason: a key nothing reads is a value an operator
// believes is in effect and is not, which is the failure that costs an
// afternoon. The third is that failure again with the override documented —
// Dial deliberately lays the forward's endpoint over `set:` (two statements
// about the destination, and the forward is the one that exists), which
// keeps the run right and still leaves the file stating a destination no
// run will ever read. Refused like the other two, because this feeds
// Lookup: a file that names two destinations is fixed, not tolerated — and
// a row Check reports but Lookup honours is the page-versus-run drift this
// package has now removed five times.
func checkSet(name, key string, conn config.Connection, ns string, inst Installed) []Problem {
	fillable := map[string]plugin.Field{}
	declared := map[string]bool{}
	// Whether this namespace has an EndpointTLS input at all — not which
	// capability declares it, matching fillable/declared's own namespace-wide
	// grain, and correct at that grain because a plugin's connFields()
	// pattern puts the same shared fields on every capability alike. If one
	// exists, a tunnel forces it to its off value unconditionally (see
	// EndpointTLS), which is the fact a TLSAdjacent field depends on below.
	hasTLSField := false
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Endpoint == plugin.EndpointTLS {
				hasTLSField = true
			}
			if f.Config == "" {
				continue
			}
			declared[f.Config] = true
			if plugin.ProfileFillable(c, f) {
				fillable[f.Config] = f
			}
		}
	}
	set := make([]string, 0, len(conn.Set))
	for k := range conn.Set {
		set = append(set, k)
	}
	sort.Strings(set)

	var problems []Problem
	for _, k := range set {
		f, ok := fillable[k]
		if !ok {
			reason := fmt.Sprintf("nothing in %s reads %q", ns, k)
			hint := "`rta explain <capability>` lists the config keys it reads"
			if declared[k] {
				// Declared but refused: a Path, or the capability's own Scope.
				// Worth its own sentence — the operator's key is not a typo,
				// the rule is deliberate, and "nothing reads it" would send
				// them looking for a spelling mistake that is not there.
				reason = fmt.Sprintf("%q is a config key in %s, but a profile may not fill it", k, ns)
				hint = "a profile chooses where a call goes, never what it reads or where it writes"
			}
			problems = append(problems, Problem{Name: name, Plugin: key, Reason: reason, Hint: hint})
			continue
		}
		if conn.Tunnelled() && f.Endpoint != plugin.EndpointNone {
			// Before the Options check, because a value nothing reads being
			// misspelt on top is not the sentence the operator needs first.
			problems = append(problems, Problem{Name: name, Plugin: key,
				Reason: fmt.Sprintf("`set: %s` is overridden by the forward `%s:` opens",
					k, conn.TunnelKey()),
				Hint: "remove it, or remove `" + conn.TunnelKey() + ":` to connect directly"})
			continue
		}
		// The harder half of the same fact: this key is not itself
		// overridden, it is inert because what it depends on was. A tunnel
		// forces this namespace's EndpointTLS input to its off value
		// unconditionally, and TLSAdjacent is a plugin's own declaration
		// that its value then does nothing — see plugin.Field.TLSAdjacent.
		if conn.Tunnelled() && f.TLSAdjacent && hasTLSField {
			problems = append(problems, Problem{Name: name, Plugin: key,
				Reason: fmt.Sprintf("`set: %s` is overridden along with the TLS mode the forward `%s:` turns off",
					k, conn.TunnelKey()),
				Hint: "remove it, or remove `" + conn.TunnelKey() + ":` to connect directly"})
			continue
		}
		// Before the Options check, because a value the handler cannot read
		// at all is the more fundamental complaint — and because Options
		// compares the text of a value, so it has nothing useful to say about
		// one that is not text.
		if problem, hint := plugin.StatedTypeProblem(f, conn.Set[k]); problem != "" {
			problems = append(problems, Problem{Name: name, Plugin: key,
				Reason: fmt.Sprintf("`set: %s` %s", k, problem), Hint: hint})
			continue
		}
		if len(f.Options) > 0 {
			if s, isStr := conn.Set[k].(string); isStr && !slicesContains(f.Options, s) {
				problems = append(problems, Problem{Name: name, Plugin: key,
					Reason: fmt.Sprintf("%q is not a value %s accepts", s, k),
					Hint:   "one of: " + strings.Join(f.Options, "|")})
			}
		}
	}
	return problems
}

// checkSecretRefs reports a well-formed `secrets:` mapping that can never
// take effect: one aimed at an input the namespace does not offer, one
// reading a cluster with no coordinate to say which, and one aimed at an
// input the coordinate's own forward fills — Dial lays the endpoint over
// everything Fill resolved, so a mapping onto an endpoint-role input beside
// `kube:` is fetched and then discarded on every call.
//
// The first two rules existed only on the run path (Fill's
// core.profile.secret.unknown, kubeSecrets' nocluster refusal), so the page
// called such a profile valid and the run refused it; the third existed
// nowhere. All three feed Lookup like checkSet's rules do, and for the same
// reason: a row Check reports but Lookup honours — or a refusal the run
// makes that the page cannot see — is the drift this package keeps removing.
// Malformed schemes are BadSecretRefs' job, already mirrored, and skipped
// here: there is nothing semantic to say about a ref that names no source.
func checkSecretRefs(name, key string, conn config.Connection, ns string, inst Installed) []Problem {
	fillable := map[string]plugin.Field{}
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if !plugin.ProfileFillable(c, f) {
				continue
			}
			// The role-carrying declaration wins the dedup, matching
			// tunnellable's "any capability declares" reading of roles.
			if cur, ok := fillable[f.Name]; !ok || cur.Endpoint == plugin.EndpointNone {
				fillable[f.Name] = f
			}
		}
	}
	var problems []Problem
	for _, ref := range conn.SecretRefs() {
		if ref.Scheme != "kv" && ref.Scheme != "kube" {
			continue
		}
		at := func(reason, hint string) {
			problems = append(problems, Problem{Name: name, Plugin: key, Reason: reason, Hint: hint})
		}
		f, ok := fillable[ref.Input]
		if !ok {
			at(fmt.Sprintf("maps a secret onto %q, which %s does not offer", ref.Input, ns),
				"`rta explain` on one of its capabilities lists the inputs it declares")
			continue
		}
		// A mapping delivers text. plugin.Request's readers do not coerce a
		// string into a number or a bool — deliberately, everywhere — so a
		// mapping onto one of those inputs resolves, authenticates, and then
		// hands the handler a zero: the quiet-garbage variant of "never takes
		// effect", worse than the loud one.
		if f.Type == plugin.Int || f.Type == plugin.Bool || f.Type == plugin.Float {
			at(fmt.Sprintf("maps a secret onto %q, which is %s — a mapping delivers text, "+
				"and the handler would read zero", ref.Input, f.Type),
				"only a text-shaped input can carry a secret's value")
			continue
		}
		if ref.Scheme == "kube" && conn.Kube == "" && conn.SecretsFrom == "" {
			hint := "name where to read it: `secrets-from: <context>/<namespace>` for a connection " +
				"that reaches its service directly, or `kube: <context>/<namespace>/svc/<name>:<port>` " +
				"when the call also needs a forward — or use `kv:` for a local entry"
			if conn.SSH != "" {
				// Following the old hint would state two tunnels, which is its
				// own refusal — say the real choice instead. `secrets-from:` is
				// open to an ssh connection, because reading a Secret is a
				// separate cluster call and not part of the forward.
				hint = "an `ssh:` tunnel cannot read a Kubernetes Secret — add " +
					"`secrets-from: <context>/<namespace>` to say which cluster holds it, " +
					"or use `kv:` for a local entry"
			}
			at(fmt.Sprintf("secrets.%s reads a credential from a cluster and does not say which",
				ref.Input), hint)
			continue
		}
		if conn.Tunnelled() && f.Endpoint != plugin.EndpointNone {
			at(fmt.Sprintf("`secrets: %s` is overridden by the forward `%s:` opens",
				ref.Input, conn.TunnelKey()),
				"remove the mapping, or remove `"+conn.TunnelKey()+":` to connect directly")
			continue
		}
		// The two blocks can target the same input — `set:` is keyed by
		// Field.Config and `secrets:` by Field.Name, and for most inputs those
		// are the same word — and when they do, Fill never fetches the
		// reference: a value Bind already supplied wins, deliberately, so that
		// nothing is fetched for an input the caller typed.
		//
		// Which leaves a `secrets:` line that resolves nothing, silently, in
		// the block whose whole purpose is that a credential comes from
		// somewhere safe. It is the failure the rest of this function reports,
		// with the more dangerous ending: an operator who moves a password out
		// of `set:` and into `secrets:` but leaves the old line behind has
		// changed nothing, and the plaintext one is still what authenticates.
		//
		// Reported rather than resolved by reordering. `set:` winning is what
		// makes `--host` beat a profile everywhere else, and quietly inverting
		// it for one block would be a precedence rule that holds in four
		// places and not the fifth.
		if f.Config != "" {
			if _, stated := conn.Set[f.Config]; stated {
				at(fmt.Sprintf("`secrets: %s` never takes effect — `set: %s` supplies that input "+
					"and a stated value wins", ref.Input, f.Config),
					"remove `set: "+f.Config+"`, which is the plaintext one, or remove the mapping")
			}
		}
	}
	return problems
}

func slicesContains(options []string, v string) bool {
	for _, o := range options {
		if o == v {
			return true
		}
	}
	return false
}
