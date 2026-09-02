package mcp

import (
	"time"

	"fmt"
	"sort"
	"strings"

	"github.com/this-is-tobi/rule-them-all/internal/config"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
	"github.com/this-is-tobi/rule-them-all/internal/lockdown"
	"github.com/this-is-tobi/rule-them-all/internal/pathguard"
	"github.com/this-is-tobi/rule-them-all/internal/profile"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Options is the operator's policy for one MCP session: which capabilities
// are exposed, which writes and destructive calls are allowed, which
// environment bounds the reach — and every question the bridge asks it.

// Options configures which capabilities are exposed.
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

	// AllowWrite names the plugins whose write-class capabilities are
	// exposed. Empty means none.
	//
	// A list of namespaces rather than the boolean this used to be. The
	// boolean was one decision, taken once at launch, for every Write
	// capability in the registry — including every one that arrives later,
	// from a plugin installed next month. An operator who enabled it for a
	// good reason (they wanted `todo add`) and pasted the result into the
	// config `rta mcp install claude` writes had issued a permanent,
	// registry-wide, update-transitive authorisation, and nothing about the
	// flag said so.
	AllowWrite []string
	// AllowDestructive lists destructive capabilities the operator has
	// allowed, each optionally pinned to the plugin artifact it came from:
	// "kv.rm", or "hello.wipe@5dae737f8845".
	//
	// A pin is REQUIRED for a capability from an external plugin and refused
	// for a built-in, because the two have different artifacts. A built-in's
	// artifact is the rta binary the operator chose to run; there is nothing
	// to pin it to that they have not already decided. An external plugin is
	// a separate file that can be replaced under the same name, and an
	// authorisation attached to the name would be inherited by whatever
	// replaces it — which is the one place in rta where a permission would
	// attach to a name rather than to an artifact, on the surface with no
	// human present.
	AllowDestructive []string
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
	// It never widens the surface. A capability the operator did not expose
	// with AllowWrite or AllowDestructive is not a tool at all, and no
	// amount of asking makes it one; this answers the grant question and
	// nothing else.
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
	// nil means every namespace is treated as unknown, which destructiveAllowed
	// refuses. That is the right zero value for a security gate: a caller who
	// forgets to wire it exposes nothing rather than everything.
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
	// wired the client up, in the same argv as --allow-write, and is trusted
	// exactly as much as those flags are.
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

// entryMatches reports whether one allowlist entry names the artifact behind
// ns, in the `name@digest-prefix` grammar trust already uses.
//
// Shared by --allow-write and --allow-destructive so the two cannot drift
// into different grammars, which they had: --allow-destructive *required* a
// pin, and --allow-write compared the whole entry as one string and therefore
// did not understand the grammar at all. An operator who learned `id@digest`
// from the flag that demands it — and whose refusal hands over the exact
// string to type — and then applied it to the other flag silently switched
// the capability off. Stating a stricter policy must never be the thing that
// turns a control off, and it must never do it in silence; Problems reports
// what could not be honoured.
//
// bareOK is the one deliberate difference between the two, not an accident.
// --allow-destructive refuses a bare entry because a destructive capability
// is exactly where "whatever binary currently answers to this name" is not
// good enough. --allow-write accepts one because namespace
// granularity there on purpose: a pin is a tightening available to an
// operator who wants it, not homework demanded of everyone.
func (o Options) entryMatches(entry, want, ns string, bareOK bool) bool {
	id, pin, pinned := strings.Cut(entry, "@")
	if id != want {
		return false
	}
	origin, known := o.origin(ns)
	if !known {
		// Neither a built-in nor a plugin this registry knows. Refused rather
		// than assumed harmless: the two things absence could mean are "built
		// in" and "never heard of it", and only the first is safe.
		return false
	}
	switch {
	case !origin.External():
		// Built-in. A pin would name an artifact that has no separate
		// identity, so accepting one would imply a check that is not
		// happening.
		return !pinned
	case !pinned:
		return bareOK
	default:
		// Prefix match, so an operator can paste the short digest rta prints
		// (always the full 12-char Origin.Short()). The 8-char floor mirrors
		// internal/plugintrust's identical digest-prefix match: below it, a
		// hand-typed or truncated pin is cheap enough to grind that it
		// degrades pinning back into "whatever replaces this name" — the one
		// thing pinning an artifact instead of a name exists to prevent.
		return len(pin) >= 8 && strings.HasPrefix(origin.Digest, pin)
	}
}

// writeAllowed reports whether this operator opened up writes for the plugin
// a capability belongs to.
func (o Options) writeAllowed(capID string) bool {
	ns := grant.Namespace(capID)
	for _, entry := range o.AllowWrite {
		if o.entryMatches(entry, ns, ns, true) {
			return true
		}
	}
	return false
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

// destructiveAllowed reports whether this operator allowed this exact
// destructive capability, from this exact artifact.
func (o Options) destructiveAllowed(capID string) bool {
	for _, entry := range o.AllowDestructive {
		if o.entryMatches(entry, capID, grant.Namespace(capID), false) {
			return true
		}
	}
	return false
}

// Problems reports allowlist entries that authorize nothing, and why.
//
// The gate is a set of string comparisons, so every way of getting one wrong
// — a typo, a namespace that is not installed, a pin left behind by an
// upgrade, a pin on a built-in — has the same outcome as deciding not to
// allow it: the capability is simply absent from tools/list. An operator who
// meant to enable something and sees nothing has no way to tell "refused"
// from "misspelled", and the agent on the other end reports only that the
// tool does not exist.
//
// Reported rather than fatal, and at startup rather than per call: the
// operator is present at `rta mcp serve` and is the only one who can act on
// it. An entry that names a plugin installed on another machine is an
// ordinary state for a shared MCP client config, exactly as it is for the
// plugins section of the config file (internal/pluginconf).
func (o Options) Problems(reg *registry.Registry) []string {
	if reg == nil {
		return nil
	}
	var out []string
	writable := map[string]bool{}
	byID := map[string]plugin.Capability{}
	for _, c := range reg.Capabilities() {
		byID[c.ID] = c
		if c.Safety == plugin.Write {
			writable[grant.Namespace(c.ID)] = true
		}
	}

	// The refused artifacts themselves, first: an allowlist entry naming one
	// is a consequence, and the operator wants the cause. Reported even when
	// no flag mentions them, because this is the only place `rta mcp serve`
	// can say it.
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

	for _, entry := range o.AllowWrite {
		ns, _, _ := strings.Cut(entry, "@")
		switch {
		case !o.knows(ns):
			out = append(out, fmt.Sprintf("--allow-write %s: %s", entry, o.absent(ns)))
		case !o.entryMatches(entry, ns, ns, true):
			out = append(out, fmt.Sprintf("--allow-write %s: %s", entry, o.pinReason(ns)))
		case !writable[ns]:
			out = append(out, fmt.Sprintf("--allow-write %s: %q has no write capabilities, so this allows nothing", entry, ns))
		}
	}
	for _, entry := range o.AllowDestructive {
		id, _, _ := strings.Cut(entry, "@")
		c, ok := byID[id]
		ns := grant.Namespace(id)
		switch {
		case !ok:
			// rta never launched an untrusted artifact, so it genuinely does
			// not know whether the capability exists — but reporting that as
			// an unknown capability names the wrong cause, and the operator's
			// pin is correct.
			if o.refused(ns) {
				out = append(out, fmt.Sprintf("--allow-destructive %s: %s", entry, o.absent(ns)))
				break
			}
			out = append(out, fmt.Sprintf("--allow-destructive %s: no capability named %q exists", entry, id))
		case c.Safety != plugin.Destructive:
			out = append(out, fmt.Sprintf("--allow-destructive %s: %q is %s, not destructive, so this allows nothing",
				entry, id, c.Safety))
		case !o.entryMatches(entry, id, ns, false):
			out = append(out, fmt.Sprintf("--allow-destructive %s: %s", entry, o.pinReason(ns)))
		}
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

// pinReason explains why a well-formed entry did not match, in the terms the
// operator has to act on: the string to type.
func (o Options) pinReason(ns string) string {
	origin, known := o.origin(ns)
	switch {
	case !known:
		return o.absent(ns)
	case !origin.External():
		return fmt.Sprintf("%q is built in and has no artifact to pin; drop the @digest", ns)
	case origin.Digest == "":
		return fmt.Sprintf("%q has no recorded digest, so no pin can match it", ns)
	default:
		return "this pin does not match the installed artifact; it is @" + origin.Short()
	}
}

// AllowFlag returns the `rta mcp serve` flag an operator needs in order to
// expose c, or "" for a capability that needs none.
//
// It exists because the artifact pin is only tolerable if the string to type
// is handed over rather than computed. A digest an operator has to go and
// look up is a control that gets turned off, so `rta explain` prints this
// verbatim and the answer is copy-pasteable.
//
// Deliberately not named DestructiveHint, which was the first name and was a
// bad one: the MCP SDK's ToolAnnotations has a *bool field by that exact
// name, set a few lines below, and two different things called the same
// thing in one file is how the wrong one gets used.
func (o Options) AllowFlag(c plugin.Capability) string {
	switch c.Safety {
	case plugin.Write:
		return "--allow-write " + o.allowValue(c)
	case plugin.Destructive:
		return "--allow-destructive " + o.allowValue(c)
	default:
		return ""
	}
}

// allowValue is the value half of AllowFlag: what the operator types after the
// flag, pinned wherever the gate requires a pin.
func (o Options) allowValue(c plugin.Capability) string {
	if c.Safety == plugin.Write {
		// Bare, because writeAllowed accepts a bare namespace (entryMatches
		// with bareOK). A pin is offered for destructive only, where the
		// artifact identity is the whole point of the gate.
		return grant.Namespace(c.ID)
	}
	if origin, known := o.origin(grant.Namespace(c.ID)); known && origin.External() {
		return c.ID + "@" + origin.Short()
	}
	return c.ID
}

// AllowValues lists everything --allow-write or --allow-destructive accepts
// for the capabilities reg holds, in the exact form the flag requires, each
// with a tab-separated description — the same shape Field.Suggest returns.
//
// Exported for one caller: shell completion for those two flags. Trust pins
// an external plugin's destructive capability to its artifact digest, and a
// digest somebody has to go and look up is a control that gets turned off.
// `rta explain` already hands the whole line over after the fact; this hands
// over the value at the moment the flag is being typed.
func (o Options) AllowValues(reg *registry.Registry, safety plugin.Safety) []string {
	if reg == nil {
		return nil
	}
	// Counted rather than named for --allow-write, because its value is a
	// namespace and the useful thing to know at that moment is how much the
	// entry opens up. --allow-destructive names one capability, so its own
	// summary is the right description.
	count := map[string]int{}
	summary := map[string]string{}
	var order []string
	for _, c := range reg.Capabilities() {
		if c.Safety != safety {
			continue
		}
		value := o.allowValue(c)
		if value == "" {
			continue
		}
		if count[value] == 0 {
			order = append(order, value)
			summary[value] = c.Summary
		}
		count[value]++
	}
	out := make([]string, 0, len(order))
	for _, value := range order {
		desc := summary[value]
		if safety == plugin.Write {
			desc = plural(count[value], "write capability")
		}
		out = append(out, value+"\t"+desc)
	}
	sort.Strings(out)
	return out
}

// plural counts a thing whose plural is not formed by adding an s.
func plural(n int, singular string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, strings.Replace(singular, "capability", "capabilities", 1))
}

// exposed reports whether a capability passes the safety gate.
func (o Options) exposed(c plugin.Capability) bool {
	switch c.Safety {
	case plugin.Read:
		return true
	case plugin.Write:
		return o.writeAllowed(c.ID)
	case plugin.Destructive:
		return o.destructiveAllowed(c.ID)
	default:
		return false
	}
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
