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
	"sort"
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
func Lookup(cfg config.Config, c plugin.Capability, name string, inst Installed) (config.Connection, *view.Error) {
	ns := plugin.Namespace(c.ID)
	none := config.Connection{}
	if !config.ValidName(name) {
		return none, view.Errorf("core.profile.invalid",
			"%q is not a valid profile name", name).
			WithHint("lowercase letters, digits and dashes, starting with a letter or digit")
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
	key, conn, covered := p.For(ns)
	if !covered {
		return none, view.Errorf("core.profile.mismatch",
			"profile %q says nothing about %s", name, ns).
			WithHint(configured(cfg, ns))
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
			WithHint("a plugin entry takes set, secrets, kube and ssh")
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
	if conn.Tunnelled() && !tunnellable(config.PluginNamespace(key), inst) {
		return none, view.Errorf("core.profile.untunnellable",
			"profile %q: %s declares no input a tunnel can fill, so the forward would "+
				"be opened and ignored", name, config.PluginNamespace(key)).
			WithHint("the call would reach the plugin's own default host, not the tunnel — " +
				"update the plugin (endpoint roles are declared by the artifact), or remove `" +
				conn.TunnelKey() + ":`")
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

// checkPin confirms a profile's plugin key names the artifact that is actually
// installed.
//
// A nil origin resolves nothing and therefore refuses every external plugin,
// which is the right zero value for a gate: a caller who forgets to wire it
// loses profiles rather than losing the check.
func checkPin(key string, inst Installed) *view.Error {
	ns, pin, pinned := strings.Cut(key, "@")
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
	case pin == "" || !strings.HasPrefix(o.Digest, pin):
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
	return endpointValues(c, tun.Endpoint), tun.Close, nil
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
		for _, key := range p.PluginKeys() {
			conn := p.Plugins[key]
			at := func(reason, hint string) {
				problems = append(problems, Problem{Name: name, Plugin: key, Reason: reason, Hint: hint})
			}
			if unknown := conn.UnknownKeys(); len(unknown) > 0 {
				at("has "+strings.Join(unknown, ", ")+", which nothing reads",
					"a plugin entry takes set, secrets, kube and ssh")
				continue
			}
			if bad := conn.BadSecretRefs(); len(bad) > 0 {
				at(strings.Join(bad, ", ")+" names no source", "write it as `kv:<entry>`")
				continue
			}
			// The same check Lookup enforces, in the same words. Two copies of
			// this rule is how `rta profile list` came to print "invalid" for
			// a profile that connected perfectly well.
			if verr := checkTunnel(conn); verr != nil {
				at(verr.Message, verr.Hint)
				continue
			}
			// The same rules Lookup enforces, in the same words, so what this
			// report calls invalid is exactly what refuses to resolve. Two
			// copies of a rule is how `rta profile list` came to print
			// "invalid" for a profile that connected perfectly well — and how
			// `rta profile show` came to print a tunnel "problem" that every
			// run sailed past.
			if conn.Tunnelled() && !tunnellable(config.PluginNamespace(key), inst) {
				at(config.PluginNamespace(key)+" declares no input a tunnel can fill, "+
					"so the forward would be opened and ignored",
					"update the plugin, or remove `"+conn.TunnelKey()+":`")
				continue
			}
			if verr := checkPin(key, inst); verr != nil {
				at(verr.Message, verr.Hint)
				continue
			}
			problems = append(problems, checkSet(name, key, conn, config.PluginNamespace(key), inst)...)
			problems = append(problems, checkSecretRefs(name, key, conn, config.PluginNamespace(key), inst)...)
		}
	}
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
func tunnellable(ns string, inst Installed) bool {
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
			if f.Endpoint != plugin.EndpointNone && plugin.ProfileFillable(c, f) {
				return true
			}
		}
	}
	return false
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
	for _, c := range capabilitiesOf(inst) {
		if plugin.Namespace(c.ID) != ns {
			continue
		}
		for _, f := range c.Inputs {
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
		if ref.Scheme == "kube" && conn.Kube == "" {
			hint := "a `kube:` secret is read from the namespace the coordinate names, so add " +
				"`kube: <context>/<namespace>/svc/<name>:<port>` — or use `kv:` for a local entry"
			if conn.SSH != "" {
				// Following the usual hint would state two tunnels, which is
				// its own refusal — say the real choice instead.
				hint = "an `ssh:` tunnel cannot read a Kubernetes Secret — use `kv:` for a " +
					"local entry, or switch the tunnel to a `kube:` coordinate"
			}
			at(fmt.Sprintf("secrets.%s reads a credential from a cluster and states no `kube:` coordinate",
				ref.Input), hint)
			continue
		}
		if conn.Tunnelled() && f.Endpoint != plugin.EndpointNone {
			at(fmt.Sprintf("`secrets: %s` is overridden by the forward `%s:` opens",
				ref.Input, conn.TunnelKey()),
				"remove the mapping, or remove `"+conn.TunnelKey()+":` to connect directly")
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
