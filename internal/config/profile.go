package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Profile is one named environment: everywhere "proj1-staging" is, across
// every plugin that has something there.
//
// A profile is three things at once, on purpose.
//
// For a person it is the fast switch — `rta use proj1-staging` and then every
// command reaches that environment's database, that environment's bucket and
// that environment's vault, with no flags at all.
//
// For an agent it is the unit of consent: naming a profile is itself what makes
// a grant required (internal/grant.Required), so an operator says "this agent
// may read proj1-staging for an hour" in one command — one command that now
// covers the whole environment rather than one plugin of it.
//
// And while one is active it is a *bound*: internal/mcp drops every grant naming
// another profile, so switching environments moves what agents can reach along
// with it. That direction is load-bearing — the selection can only ever
// subtract. An agent still names the profile in the call, the call is still
// filled from the name it gave, and a grant is still what permits it; session
// state adds nothing to any of those and can only refuse. A convenience that
// could grant would be a permission system nobody can see.
//
// Switching *off* is therefore the absence of the bound, not a stronger one:
// grants alone decide again. `rta grant revoke` is how reach is taken away.
//
// There is deliberately no `agents:` key. A profile is agent-reachable when,
// and only while, a human-issued grant names it — expiring, renewable,
// revocable and visible in `rta grant list`. A per-profile policy key would be
// a second permission system that never lapses and therefore never renews,
// which is the opposite of what this is for.
type Profile struct {
	// Plugins is what this environment is, keyed the way a top-level plugins:
	// section is keyed: a namespace for a built-in, "pg@1a2b3c4d" for anything
	// on $PATH.
	//
	// Pinned, and this is not optional. Plugin registration is first-come and
	// $PATH decides the order, which is why plugins: sections are keyed this
	// way already. A profile keyed on a bare namespace would hand a $PATH
	// impostor declaring Name: "pg" the operator's stated connection values —
	// and, once a profile can carry cluster coordinates, a credential read
	// from their cluster and a forward into it.
	//
	// Same grammar as the top-level block on purpose: a profile is a plugins:
	// section that only applies while you are in that environment, so it is one
	// shape to learn rather than two, and the same validation reports the same
	// problems in the same words.
	Plugins map[string]Connection `yaml:"plugins,omitempty" json:"plugins,omitempty"`

	// Note is why this environment exists, printed by `rta profile list`,
	// `rta grant list --detail`, `rta use` and `rta doctor`.
	Note string `yaml:"note,omitempty" json:"note,omitempty"`

	// TTL is how long an activation of this profile lasts by default — "2h",
	// "30m", or empty for no deadline. `rta use --for` overrides it.
	//
	// It lives with the profile rather than only on the command because the
	// profile is what knows how dangerous it is. A production environment
	// declares `ttl: 1h` once and every activation of it is temporary
	// thereafter, including the ones somebody makes in a hurry from the TUI
	// with a single keypress. A deadline that has to be remembered is a
	// deadline that gets forgotten exactly when it matters.
	TTL string `yaml:"ttl,omitempty" json:"ttl,omitempty"`

	// unknown is every key in this profile that no field above claims,
	// computed by the loader against the raw document.
	//
	// A config file is read on every invocation with nobody watching, and a key
	// nothing consumes is a value the operator believes is in effect. That is
	// the failure `rta doctor` already reports for `plugins:` sections and for
	// theme fields; a profile is where it costs the most, because the thing
	// quietly not happening is *which server this call reaches*. `plguin: pg`
	// is one keystroke from a working profile and, without this,
	// indistinguishable from one.
	//
	// Computed rather than collected with a `,inline` map, which goccy fills
	// with *every* key including the claimed ones — so an inline field reports
	// `plugins` and `note` as unknown and every valid profile becomes invalid.
	// Found immediately, by the tests that already existed.
	unknown []string

	// trusted records that this profile came from a config path somebody
	// named, rather than from the ./.rta.yaml fallback. Unexported and set by
	// the loader: a file must not be able to declare itself trustworthy.
	trusted bool
}

// Connection is what one profile says about one plugin.
type Connection struct {
	// Set overlays that plugin's own configuration. Keys are Field.Config keys
	// — the identical grammar already legal under plugins.<ns>: — so the same
	// validation reports the same problems, and vault's two inputs both named
	// "mount" stay distinguishable as kv-mount and transit-mount.
	Set map[string]any `yaml:"set,omitempty" json:"set,omitempty"`

	// Secrets maps a declared input onto where its value comes from.
	//
	//	secrets:
	//	  password: kv:prod-db-password        # this release
	//	  password: kube:postgres-creds/password  # stage 2
	//
	// **A reference, never a value.** `Config` is refused on a `Secret`
	// input because a config file is plaintext read on every invocation with
	// nobody watching; that argument is untouched here, because what this holds
	// is a name. The value is fetched at resolution time and travels the path
	// every credential already travels, reaching neither this file, nor an
	// environment, nor an argv.
	//
	// One block with a scheme rather than one key per source, so `kv:` today
	// and `kube:` at stage 2 are one thing an operator learns once — and so
	// `rta profile show` has a single table to print rather than two shapes
	// that mean the same thing.
	//
	// The operator writes this mapping and the plugin never does,
	// unchanged. A plugin that could name the entry it wanted could name any
	// entry in the store; here it declares only that it has a Secret input.
	Secrets map[string]string `yaml:"secrets,omitempty" json:"secrets,omitempty"`

	// Kube is the port-forward coordinate, context/namespace/kind/name:port.
	//
	// A call filled from a connection stating this runs through a
	// `kubectl port-forward` the host raises and tears down again, and the
	// plugin sees an ordinary local address in whichever inputs it declared
	// with a plugin.Field.Endpoint role. It never learns a forward was there,
	// which is the tunnel contract and the reason no service plugin changes to
	// gain this.
	//
	// One forward per call, torn down after. Caching one across calls is an
	// optimisation with a correctness cost: a port-forward outlives the pod it
	// points at, so a cached tunnel to a rescheduled pod fails in a way nobody
	// can read. Measured against a real cluster at 54 ms median to open, which
	// is the cost that has to stay worth it.
	Kube string `yaml:"kube,omitempty" json:"kube,omitempty"`

	// SSH is the jump-host target, [user@]host[:port]/desthost:destport —
	// the other spelling of the same fact, for the service that lives behind
	// a bastion rather than in a cluster. The head is an ~/.ssh/config alias
	// or hostname and everything the operator's config says about it keeps
	// working (rta shells out to ssh); the tail is what the far end dials.
	// Same contract as Kube in every direction: one forward per call, the
	// plugin sees a local address in its endpoint-role inputs and never
	// learns a tunnel was there, and a connection states at most one of the
	// two — Check refuses both at once.
	SSH string `yaml:"ssh,omitempty" json:"ssh,omitempty"`

	// unknown is every key in this connection no field above claims. Same
	// argument as Profile.unknown, one level down — and the level a migration
	// lands on, since the old single-plugin shape put `set:` where `plugins:`
	// now goes.
	unknown []string
}

// Tunnelled reports whether a call filled from this connection runs through a
// forward — either scheme. Every rule about "the forward fills the endpoint
// inputs" hangs off this one predicate rather than off a scheme's own field,
// because a second scheme re-deriving each rule per scheme is exactly how the
// `secrets:` twin of the `set:` shadowing rule went unwritten the first time.
func (c Connection) Tunnelled() bool { return c.Kube != "" || c.SSH != "" }

// TunnelKey names the key that makes this connection tunnelled — "kube" or
// "ssh", "" when neither — so a message can tell the operator which line to
// remove without guessing. A connection stating both is refused by Check
// before any message needs a single answer; this prefers `kube:` merely to be
// deterministic on that half-broken input.
func (c Connection) TunnelKey() string {
	switch {
	case c.Kube != "":
		return "kube"
	case c.SSH != "":
		return "ssh"
	}
	return ""
}

// SecretRef is one entry of Secrets, split into the scheme and what follows.
type SecretRef struct {
	// Input is the declared input this fills.
	Input string
	// Scheme is "kv" or "kube".
	Scheme string
	// Ref is the rest: an entry name for kv, "<secret>/<key>" for kube.
	Ref string
}

// SecretRefs parses Secrets into sorted, split references. A value with no
// recognised scheme yields Scheme "", which Check and Lookup both refuse — a
// bare entry name would otherwise be ambiguous the day a second source exists,
// and guessing which one an operator meant is not a guess worth making about a
// credential.
func (c Connection) SecretRefs() []SecretRef {
	out := make([]SecretRef, 0, len(c.Secrets))
	for input, ref := range c.Secrets {
		scheme, rest, ok := strings.Cut(ref, ":")
		if !ok {
			scheme, rest = "", ref
		}
		out = append(out, SecretRef{Input: input, Scheme: scheme, Ref: rest})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Input < out[j].Input })
	return out
}

// BadSecretRefs reports the entries of Secrets whose scheme rta does not
// recognise at all, sorted.
func (c Connection) BadSecretRefs() []string {
	var out []string
	for _, ref := range c.SecretRefs() {
		switch ref.Scheme {
		case "kv", "kube":
		default:
			out = append(out, "secrets."+ref.Input)
		}
	}
	return out
}

// UnknownKeys lists the keys in this connection that no field claims, sorted.
func (c Connection) UnknownKeys() []string { return c.unknown }

// UnknownKeys lists the keys in this profile that no field claims, sorted.
func (p Profile) UnknownKeys() []string { return p.unknown }

// Trusted reports whether this profile came from a config path somebody named.
func (p Profile) Trusted() bool { return p.trusted }

// profileKeys is every key a profile may carry, and connectionKeys every key
// one of its plugin entries may carry. The loader compares the raw document
// against these, so a field added above without a line here is reported as
// unknown by the very next test run rather than silently accepted.
var (
	profileKeys    = map[string]bool{"plugins": true, "note": true, "ttl": true}
	connectionKeys = map[string]bool{"set": true, "secrets": true, "kube": true, "ssh": true}
)

// unclaimed lists the keys of a raw block that no field of the shape claims,
// sorted. The loader's half of the "a key nothing reads is a lie" rule.
func unclaimed(raw map[string]any, claimed map[string]bool) []string {
	var out []string
	for key := range raw {
		if !claimed[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

// legacyProfileKeys are the keys of the single-plugin shape profiles had
// before they became environments. Recognised by name so the failure is a
// migration instruction rather than "plugin, which nothing reads" — an
// operator whose file stopped working deserves to be told what it became.
var legacyProfileKeys = map[string]bool{"plugin": true, "set": true, "secrets": true, "kube": true}

// Legacy reports whether this profile is written in the pre-environment shape.
func (p Profile) Legacy() bool {
	for _, k := range p.unknown {
		if legacyProfileKeys[k] {
			return true
		}
	}
	return false
}

// Namespaces lists the plugin namespaces this profile configures, without
// pins, sorted.
func (p Profile) Namespaces() []string {
	out := make([]string, 0, len(p.Plugins))
	for key := range p.Plugins {
		ns, _, _ := strings.Cut(key, "@")
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// PluginKeys lists this profile's plugin entries as written, sorted.
func (p Profile) PluginKeys() []string {
	out := make([]string, 0, len(p.Plugins))
	for key := range p.Plugins {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// For finds what this profile says about a namespace, and the key it was
// written under.
//
// By namespace rather than by exact key because the pin is checked separately,
// against what is actually installed — a stale pin has to produce "this pin
// does not match" and not "this profile is silent about pg", which would send
// the call to the base connection with no complaint at all.
func (p Profile) For(namespace string) (string, Connection, bool) {
	for _, key := range p.PluginKeys() {
		if ns, _, _ := strings.Cut(key, "@"); ns == namespace {
			return key, p.Plugins[key], true
		}
	}
	return "", Connection{}, false
}

// Covers reports whether this profile says anything about a namespace.
func (p Profile) Covers(namespace string) bool {
	_, _, ok := p.For(namespace)
	return ok
}

// Window is how long an activation of this profile lasts, and whether it has a
// deadline at all. An unparseable TTL yields no deadline and is reported as a
// problem by Check — a profile is not the place to guess.
func (p Profile) Window() (time.Duration, bool) {
	if p.TTL == "" {
		return 0, false
	}
	d, err := time.ParseDuration(p.TTL)
	if err != nil || d <= 0 {
		return 0, false
	}
	return d, true
}

// BadTTL reports whether TTL was written and cannot be read.
func (p Profile) BadTTL() bool {
	if p.TTL == "" {
		return false
	}
	_, ok := p.Window()
	return !ok
}

// PluginNamespace is the namespace half of a plugins: key, pin removed.
func PluginNamespace(key string) string {
	ns, _, _ := strings.Cut(key, "@")
	return ns
}

// profileName is what a profile may be called: lowercase, digits and dashes,
// starting with an alphanumeric.
//
// Closed deliberately. The name reaches an environment variable
// (RTA_PROFILE_<P>_<INPUT>) and a grant record, and it arrives over MCP from
// an agent — three places where "whatever the operator typed" is the wrong
// amount of freedom.
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidName reports whether name is a legal profile name.
func ValidName(name string) bool { return profileName.MatchString(name) }

// ProfileNames lists the configured profiles, sorted.
func (c Config) ProfileNames() []string {
	out := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ProfilesFor lists the configured profiles that configure namespace, sorted.
func (c Config) ProfilesFor(namespace string) []string {
	var out []string
	for _, name := range c.ProfileNames() {
		if c.Profiles[name].Covers(namespace) {
			out = append(out, name)
		}
	}
	return out
}

// trustedPath reports whether the config file rta just read is one somebody
// named, rather than the working-directory fallback.
//
// config.Path() falls back to ./.rta.yaml when os.UserConfigDir() fails —
// ordinary under `env -i`, inside a container, and in CI — so without this a
// cloned repository could ship a .rta.yaml defining a profile called "prod"
// pointing at the operator's own cluster, and `rta pg query --profile prod`
// would reach it. A plugins: section has the same shape but predates this and
// is left alone; the rule is scoped to the new block, where the whole point is
// that a name stands for somewhere else.
// TrustedPath is trustedPath for the surfaces that write a profile rather
// than read one.
//
// A profile written into a file nothing honours is a silent no-op: the write
// succeeds, `rta profile show` prints it back, and every command ignores it.
// The command that writes has to be able to ask the question before it writes,
// and it cannot ask the profile — the answer is stamped onto a profile by the
// loader, which has not run yet on something being created.
func TrustedPath() bool { return trustedPath() }

func trustedPath() bool {
	if os.Getenv("RTA_CONFIG") != "" {
		return true
	}
	_, err := os.UserConfigDir()
	return err == nil
}
