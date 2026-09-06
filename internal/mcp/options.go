package mcp

import (
	"time"

	"fmt"
	"sort"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Options is the operator's policy for one MCP session: which environment
// bounds the reach, which roots confine a path, who is on the other end —
// and every question the bridge asks it.
//
// What is *not* here any more is an exposure allowlist. A capability that
// changes anything costs a grant a person issued (internal/grant.Required),
// and there is no flag that stands in for one.
type Options struct {
	// guardPin is the guard state NewServer observed at startup, checked
	// before any grant is honoured — see the comment beside grant.Reserve in
	// bridge.go. Unexported on purpose: it is this package's own snapshot,
	// not a knob, and a caller who could set it could also set it wrong.
	guardPin guard.Pin

	// locks is this process's live view of the frozen principals, checked
	// before every other gate on every call — see the comment at the top of
	// call(). Unexported for guardPin's reason; set by NewServer.
	locks *lockdown.Pin

	// Session is the id this process stamps on every ledger entry, and
	// Connected is told the client's announced name once the MCP handshake
	// completes — wired by the command that starts the server, so that
	// "this server records its presence" is a line somebody typed.
	Session   string
	Connected func(client string)

	// Consent turns on just-in-time consent: a call the grant
	// gate refuses for want of a grant is parked, the operator is asked,
	// and it proceeds or is refused with the answer they gave.
	//
	// Off by default, and the default is the important half: a parked call
	// in a server nobody is watching is worse than a refusal, and an
	// operator running headless has said by their absence that there is
	// nobody to ask. With it off, every refusal is exactly what it is
	// today.
	//
	// It never widens the surface. A HumanOnly capability is not a tool at
	// all, and no amount of asking makes it one; this answers the grant
	// question and nothing else.
	Consent bool
	// ConsentWait bounds how long a parked call waits. Zero means
	// consent.DefaultWait.
	ConsentWait time.Duration
	// ConsentNotify rings this machine's desktop notification when a call
	// parks, because a control nobody notices is a control that times out.
	//
	// Separately off by default, and not folded into Consent: a background
	// process that raises OS notifications without having been asked to is
	// behaving like malware, and the operator who wants consent on a server
	// they watch through its log is not asking for their screen to be
	// interrupted. What it shows is rta's own words only — see
	// ringDoorbell.
	ConsentNotify bool
	// ConsentPreview runs a destructive call's own --dry-run before parking
	// it, so the operator answers a question about an outcome rather than
	// about an intention.
	//
	// On by default where consent is on, and bounded to built-in
	// capabilities: see propose. A dry run is an extra invocation of the
	// handler, so it rests on the handler being honest about DryRun — true
	// of rta's own by test, a claim for anybody else's.
	ConsentPreview bool
	// Origin answers where a namespace came from. It is the registry's
	// method, passed in rather than a map built beside it, so the gate and
	// the catalogue cannot disagree about what is registered — which they
	// did, once, and once was enough: a plugin that stayed registered while
	// dropping out of the plugin host's bookkeeping was read here as a
	// built-in, and a built-in needs no digest pin.
	//
	// nil means every namespace is treated as unknown, which artifact reads
	// as "no digest" — so a grant issued against a real artifact stops
	// covering the call. That is the right zero for a security gate: a
	// caller who forgets to wire it authorizes nothing rather than
	// everything.
	Origin func(namespace string) (registry.Origin, bool)
	// Config answers what the operator stated for a namespace, already
	// matched to the artifact by internal/pluginconf. nil means nothing is
	// configured, which is the right zero here for the opposite reason to
	// Origin's: forgetting to wire it withholds values rather than granting
	// any, so the failure is a capability that asks for an argument it could
	// have had, not one that reaches somewhere nobody authorised.
	Config func(namespace string) map[string]any
	// Secrets fetches a `secrets:` reference for a profile. nil means this
	// server resolves none, which withholds credentials rather than granting
	// any — a profile that needs one then fails to connect, saying so.
	//
	// Injected rather than imported so this package never depends on the
	// store, and so a caller has to decide, in one line somebody typed, that
	// this server may open it.
	Secrets profile.Reader
	// Profiles is the operator's configured connections, as read when the
	// server was built. The zero value has none, which withholds reach rather
	// than granting it: with no profiles configured, no tool advertises the
	// property, `additionalProperties: false` refuses the name outright, and
	// every capability behaves exactly as it did before profiles existed.
	//
	// It is the *schema's* answer, and only that. What a call resolves through
	// comes from Reload — see there.
	Profiles config.Config
	// Reload answers the operator's connections as the config file has them
	// now. nil falls back to the startup snapshot.
	//
	// **Because a snapshot made the operator's own file stop being the truth,
	// and a pin turned that from a curiosity into a lockout.** A grant is
	// issued by `rta grant allow`, which reads the file; it is compared
	// against the connection this server would resolve. While that came from a
	// snapshot, editing a profile under a running server put the two beyond
	// reach of each other permanently: the re-issued grant carried the file's
	// stamp, the server kept computing the startup one, and every call was
	// refused with the sentence an ungranted call gets — while `rta grant
	// list` and `rta doctor`, which also read the file, reported the grant
	// healthy. The operator followed the documented remedy, watched it not
	// work, and had nothing anywhere to tell them why.
	//
	// Reading per call is what every other input to this decision already
	// does: grants are loaded per call, and so is the operator's selection.
	// The schema stays a snapshot because it is sent once, so a profile added
	// after startup is not advertised until a restart — and a call naming it
	// is refused rather than mis-resolved, which is the right direction.
	Reload func() config.Config
	// Active answers which environment the operator has switched on, read per
	// call so that switching takes effect in a server that has been running for
	// hours. nil means the real selection (internal/profile.Active) — the
	// default is the enforcement, and a test that wants none says so.
	//
	// It can only *narrow*. While something is on, a call naming any other
	// profile is refused with the same sentence an ungranted one gets. It never
	// supplies a profile to a call that named none, never satisfies R5, and
	// never turns a refusal into an approval. That direction is the entire
	// reason session state is admissible here at all: an expanding session
	// input would let a person's consent and the connection actually touched
	// disagree, while a subtracting one cannot.
	Active func() string
	// Untrusted names the plugin artifacts discovery found on $PATH and
	// refused to launch, because nothing has approved them.
	//
	// Names rather than the pluginhost type, so this package keeps depending
	// on nothing that spawns a process. It exists for one reason: `rta mcp
	// serve` is the surface where nobody sees the startup line — it is written
	// to a pipe the client owns — and an agent reporting "no such tool" is
	// indistinguishable from a plugin that was never installed. The operator
	// is present at the command that starts this server, and is the only one
	// who can act on it.
	Untrusted []string
	// Paths confines every caller-supplied path argument. Nil allows
	// everything, which is what the tests that predate it do and what no
	// server should.
	Paths *pathguard.Guard
	// Agent is the name this server was launched under (`--as claude-desktop`),
	// empty when the operator did not name it.
	//
	// **It is the operator's word, not the client's.** An MCP client announces
	// a name for itself in the initialize handshake and rta records that too
	// — beside this, never instead of it, because a name a thing chooses for
	// itself is not an identity. This one is typed where the operator
	// wired the client up, in the same argv as --root, and is trusted exactly
	// as much as that flag is.
	//
	// It reaches the gate as grant.Caller.Agent, where it can only subtract:
	// a named server matches only grants issued to that name, and an unnamed
	// one matches only grants issued to no name. See grant.Grant.Agent.
	Agent string
	// Remote marks a server reached over the HTTP transport rather than
	// stdio — a caller on a different machine from the one rta runs on.
	//
	// It narrows what exposed already narrowed: a capability marked
	// plugin.Capability.HostSpecific describes the machine rta happens to
	// run on, which is the operator's own machine under stdio (the client
	// launched this process) and is not under HTTP (a network caller is
	// never the machine). False for stdio, which is every server this field
	// existed before and changes nothing for.
	Remote bool
}

// active is the profile switched on right now, or "".
func (o Options) active() string {
	if o.Active == nil {
		return profile.Active()
	}
	return o.Active()
}

// connStamp fingerprints the connection a call on this profile and namespace
// would be filled from, for the grant gate to compare a pin against.
//
// **Read from o.Profiles, the same config the fill will use.** internal/app
// loads it once at startup, deliberately, so that a profile removed from the
// file stops being reachable at the next server start. Stamping from a fresh
// read instead would refuse every call on an environment edited since this
// process began — with "re-issue the grant" offered as a remedy that could
// never work, because the server would go on computing the older stamp. Taken
// from what the call will actually resolve through, a mismatch means the one
// thing it should: this grant was consented to for a different connection.
//
// A name that resolves to nothing stamps as empty, which no profiled grant
// carries, so the call is refused. That is the right zero for a gate: an
// unknown profile is not a reason to skip a check.
func (o Options) connStamp(name, namespace string) string {
	return profile.ConnStampFor(o.profiles(), name, namespace)
}

// profiles is the connection set a call resolves through: the file as it is
// now, or the startup snapshot when no reader was wired.
func (o Options) profiles() config.Config {
	if o.Reload == nil {
		return o.Profiles
	}
	return o.Reload()
}

// origin resolves a namespace, treating an unwired lookup as "nothing is
// known", which is the fail-closed direction.
func (o Options) origin(ns string) (registry.Origin, bool) {
	if o.Origin == nil {
		return registry.Origin{}, false
	}
	return o.Origin(ns)
}

// pluginConfig is the operator's stated values for the plugin a capability
// belongs to.
//
// An agent's call gets them, and that is the point rather than an oversight:
// the operator configured this plugin, so `pg.query` reaching the configured
// database is what they asked for, and an MCP surface where config silently
// does not apply would be the same asymmetry rta keeps removing. Nothing
// sensitive rides here — pkg/plugin refuses Config on a Secret input — and
// Local inputs are still stripped from incoming arguments and resolved from
// the server's own environment.
//
// A nil Config is "none", which is the correct and safe zero: an Options
// nobody filled in hands every plugin nothing, rather than handing every
// plugin everything.
func (o Options) pluginConfig(c plugin.Capability) map[string]any {
	if o.Config == nil {
		return nil
	}
	words := c.Words()
	if len(words) == 0 {
		return nil
	}
	return o.Config(words[0])
}

// Problems reports what this server could not honour, and why.
//
// Reported rather than fatal, and at startup rather than per call: the
// operator is present at `rta mcp serve` and is the only one who can act on
// it. A plugin installed on another machine is an ordinary state for a
// shared MCP client config, exactly as it is for the plugins section of the
// config file (internal/pluginconf).
func (o Options) Problems(reg *registry.Registry) []string {
	if reg == nil {
		return nil
	}
	var out []string
	// An artifact discovery found and declined to launch. This is the only
	// place `rta mcp serve` can say it: an agent reporting "no such tool" is
	// indistinguishable from a plugin that was never installed.
	for _, ns := range o.Untrusted {
		// A name something already answers to is a different problem, and
		// saying "none of its capabilities are exposed" about it would be
		// false — the registered plugin's tools are in tools/list.
		if o.knows(ns) {
			out = append(out, fmt.Sprintf(
				"an artifact on $PATH named %q was found and not run, and %q is already "+
					"registered — trusting it would collide rather than add anything; remove "+
					"the file or rename it", ns, ns))
			continue
		}
		out = append(out, fmt.Sprintf(
			"plugin %q is installed and was not run, so none of its capabilities are exposed"+
				" — `rta plugin trust %s` approves it, and the next server start loads it", ns, ns))
	}

	return out
}

func (o Options) knows(ns string) bool {
	_, known := o.origin(ns)
	return known
}

// absent explains why a namespace is not registered, telling "not installed"
// apart from "installed and not run".
//
// The distinction is the difference between two afternoons: one is a missing
// install to chase, the other is a one-line approval. Told the wrong one, an
// operator goes to check their spelling, their $PATH and their install for a
// plugin rta can see, has hashed, and is deliberately declining to run — with
// the digest they pinned appearing, correctly, in rta's own "not installed"
// message.
func (o Options) absent(ns string) string {
	// knows(ns) is false on every path that reaches here, so a refused
	// artifact whose name is taken cannot be reported by this function.
	if o.refused(ns) {
		return fmt.Sprintf("%q is installed and has not been run; `rta plugin trust %s` approves it", ns, ns)
	}
	return fmt.Sprintf("no plugin named %q is installed", ns)
}

// refused reports whether this namespace names an artifact discovery found and
// declined to launch.
func (o Options) refused(ns string) bool {
	for _, u := range o.Untrusted {
		if u == ns {
			return true
		}
	}
	return false
}

// exposed reports whether a capability is a tool on this server at all.
//
// One question now, where there used to be two. A capability for the person
// at the terminal is never a tool; everything else is, and what it costs to
// *call* is a grant (internal/grant.Required) rather than a flag typed once
// into a client's config file. The safety class still decides that price —
// a read is free, a write is not — it just no longer decides visibility.
func (o Options) exposed(c plugin.Capability) bool {
	return !c.HumanOnly
}

// artifact is the digest of the plugin behind a namespace, empty for a
// built-in — what a grant issued against this namespace has to match.
//
// A namespace this registry does not know also answers empty, and that is
// fail-closed rather than a hole: a grant issued while the plugin *was*
// known carries its digest, so the mismatch refuses the call. The reverse
// drift refuses too. See grant.Grant.Digest.
func (o Options) artifact(ns string) string {
	origin, known := o.origin(ns)
	if !known || !origin.External() {
		return ""
	}
	return origin.Digest
}

// remoteExposed reports whether a capability passes the locality gate: a
// second, independent question from exposed's safety-class one.
//
// Registration-time, like exposed, and for the same reason: a capability
// that fails this is absent from tools/list, not present-and-refusing, so an
// agent behind a remote instance never sees sys_overview or git_status as
// something to try. There is no allowlist to widen this with — an operator
// who wants a HostSpecific capability from the box rta runs on has SSH.
func (o Options) remoteExposed(c plugin.Capability) bool {
	return !o.Remote || !c.HostSpecific
}

// RemoteBlocked lists every capability the locality gate hides, for the
// startup banner: what Remote actually did, named, rather than left for an
// operator to infer from a shorter tools/list or an agent to discover as a
// tool that quietly is not there.
//
// Scoped to what would otherwise be visible — a HostSpecific capability the
// safety gate was already refusing (an unallowed destructive one, say) is
// not this gate's doing, and naming it here would double-count a different
// refusal as if Remote had caused it.
func (o Options) RemoteBlocked(reg *registry.Registry) []string {
	if !o.Remote || reg == nil {
		return nil
	}
	var out []string
	for _, c := range reg.Capabilities() {
		if c.HostSpecific && o.exposed(c) {
			out = append(out, c.ID)
		}
	}
	sort.Strings(out)
	return out
}
