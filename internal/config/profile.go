package config

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// colorPattern is the grammar a profile's `color:` must match — the same one
// schemaColor states to an editor and theme.HexColor enforces for `theme:`.
var colorPattern = regexp.MustCompile(schemaColor)

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

	// Color marks this environment so that which one you are in is legible
	// before the command runs rather than after it. `#rrggbb`, the same
	// grammar `theme:` uses.
	//
	// **rta paints exactly one thing with it: the profile's own name.** A
	// profile that could repaint the palette would put its colour on keys,
	// labels and selection — beside Good, Warn and Bad, which mean something
	// — and a production environment marked red would then draw healthy rows
	// in the colour of a failure. That is worse than no marking at all,
	// because it teaches the eye to ignore red. One badge, in one place,
	// cannot be misread as a status.
	//
	// Opt-in, and that is what lets the badge print on every command without
	// becoming noise: writing this line is the operator saying *this* is the
	// environment worth interrupting me about. A profile with no colour
	// behaves exactly as it did before.
	//
	// A colour that is not a colour is reported by `rta doctor` and otherwise
	// ignored — never a reason to refuse the profile. BadTTL refuses because
	// the consequence of a missing deadline is a production switch that never
	// lapses; the consequence here is a badge that is not painted, and
	// refusing to reach production over a mistyped hex would be the tool
	// inventing an outage.
	Color string `yaml:"color,omitempty" json:"color,omitempty"`

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

	// SecretsFrom names the cluster and namespace a `kube:` secret reference
	// is read from, as `context/namespace`, for a connection that reaches its
	// service directly.
	//
	// **It exists because `kube:` was carrying two unrelated facts.** A
	// coordinate says both "read this connection's Secrets from here" and
	// "open a forward to this service", and only the first two of its four
	// segments are used for the read — tunnel.Secrets discards kind, name and
	// port. That conflation had a consequence nothing in the codebase ever
	// argued for: a credential kept in a cluster forced the connection through
	// a port-forward, because stating the coordinate needed to read it also
	// laid that coordinate's endpoint over `set: host`. A database on a public
	// address whose password lives in a cluster Secret was simply not
	// expressible.
	//
	// So this is the read half on its own, and `kube:` keeps meaning exactly
	// what it did — a forward. A connection states at most one of the two:
	// with `kube:` present the Secrets already come from its namespace, and
	// naming a second source would be two statements about one fact, which
	// Check refuses the way it refuses `kube:` and `ssh:` together.
	//
	// The namespace is still the connection's own and never a caller's, which
	// is the invariant that mattered all along: a reference reaching into a
	// different namespace would turn one connection's coordinate into a
	// general-purpose cluster reader.
	SecretsFrom string `yaml:"secrets-from,omitempty" json:"secrets-from,omitempty"`

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

	// TunnelTLS states that the far side of this connection's forward speaks
	// TLS on its own, so a plugin.EndpointURL input should be filled with
	// `https://` instead of the tunnel's ordinary `http://`.
	//
	// **Named apart from every plugin's own TLS field on purpose.** etcd,
	// pg, mysql, mariadb, qdrant and s3 each declare their own `tls` or
	// `sslmode` — a plugin.EndpointTLS-role field naming *that plugin's*
	// on/off spelling in its own client library's vocabulary (libpq's
	// `sslmode`, go-sql-driver's `tls=`), forced to "off" by the host over a
	// tunnel. This is a different fact at a different layer: not a plugin's
	// own setting, but what the host must know about the *coordinate itself*
	// to fill plugin.EndpointURL correctly, before any plugin config is even
	// read. Sharing the word "tls" with those would read as one concept and
	// mean two, right where a profile stacks a connection's own `set: {tls:
	// ...}` beside `kube:`/`ssh:` — etcd's `tls` and qdrant's are exactly this
	// shape one level down.
	//
	// **A statement about the destination, never about the hop that carries
	// it.** `kubectl port-forward` and `ssh -L` are both a raw byte pipe from
	// 127.0.0.1 straight into whatever the far end's socket speaks — neither
	// terminates or re-originates a request. plugin.EndpointURL's own default
	// of plain http reasons correctly about the tunnel itself (a loopback hop
	// that never leaves the machine) and says nothing about the process
	// listening at the other end. A service that terminates TLS at that
	// socket — Vault's own listener, unlike PostgreSQL or MySQL, has no
	// plaintext fallback to negotiate down to — needs to be told so
	// explicitly, because nothing about the forward lets the host guess it.
	//
	// Opt-in and default false, matching EndpointURL's own existing default:
	// the overwhelming majority of tunnelled services are the plaintext-behind
	// TLS-transport case that default already reasons about correctly, and
	// flipping the default would ask every one of them to state
	// `tunnelTLS: false` instead of the minority stating `tunnelTLS: true`.
	//
	// Verifies the certificate rta actually receives — this is not
	// InsecureSkipVerify, and does not become it. A self-signed or
	// cluster-internal CA the client does not already trust still refuses,
	// with the same `*.tls.untrusted` a direct connection would get; a plugin
	// that reads a CA bundle of its own (vault's `ca-file`, following etcd's)
	// is how that gets resolved, not this flag.
	TunnelTLS bool `yaml:"tunnelTLS,omitempty" json:"tunnelTLS,omitempty"`

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
	profileKeys = map[string]bool{"plugins": true, "note": true, "ttl": true,
		"color": true}
	connectionKeys = map[string]bool{"set": true, "secrets": true, "kube": true, "ssh": true,
		"secrets-from": true, "tunnelTLS": true}
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
// pins or instance labels, deduplicated and sorted. Deduplicated because two
// instances of one plugin are one namespace — the question this answers is
// "which plugins does this environment touch", and `pg, pg` is not an answer.
func (p Profile) Namespaces() []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(p.Plugins))
	for key := range p.Plugins {
		ns, _, _ := SplitKey(key)
		if !seen[ns] {
			seen[ns] = true
			out = append(out, ns)
		}
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

// SplitKey splits a plugins: key into its namespace, instance label and pin:
// "pg" → (pg, "", ""), "pg@1a2b" → (pg, "", 1a2b), "pg/analytics@1a2b" →
// (pg, analytics, 1a2b).
//
// The one splitter, exported, because four call sites were already cutting
// "@" by hand when the instance segment arrived, and a fifth doing it from
// memory is how "pg/analytics" gets looked up as a namespace. An instance
// label is how one profile holds several connections to the same plugin —
// staging's main database and its analytics one — without a second grammar:
// the label rides inside the key that already existed, the pin stays where
// it was, and a bare key keeps meaning what it always meant, the default
// instance.
func SplitKey(key string) (ns, instance, pin string) {
	head, pin, _ := strings.Cut(key, "@")
	ns, instance, _ = strings.Cut(head, "/")
	return ns, instance, pin
}

// ValidInstance reports whether s is a legal instance label. The grammar is
// deliberately the plugin-name grammar — the label sits in the key position a
// namespace owns, reaches grant records and audit lines through a profile
// ref, and "whatever the operator typed" is the wrong amount of freedom in
// all three places.
func ValidInstance(s string) bool { return plugin.ValidName(s) }

// Instances lists the instance labels this profile holds for a namespace,
// sorted — which places "" first when an unlabeled default exists. Empty when
// the profile says nothing about the namespace at all.
func (p Profile) Instances(namespace string) []string {
	var out []string
	for _, key := range p.PluginKeys() {
		if ns, instance, _ := SplitKey(key); ns == namespace {
			out = append(out, instance)
		}
	}
	sort.Strings(out)
	return out
}

// ForInstance finds the entry for exactly this namespace and instance label,
// and the key it was written under. "" names the unlabeled default entry.
func (p Profile) ForInstance(namespace, instance string) (string, Connection, bool) {
	for _, key := range p.PluginKeys() {
		if ns, inst, _ := SplitKey(key); ns == namespace && inst == instance {
			return key, p.Plugins[key], true
		}
	}
	return "", Connection{}, false
}

// For finds what this profile says about a namespace when the caller names no
// instance, and the key it was written under.
//
// By namespace rather than by exact key because the pin is checked separately,
// against what is actually installed — a stale pin has to produce "this pin
// does not match" and not "this profile is silent about pg", which would send
// the call to the base connection with no complaint at all.
//
// The default rules, in order: an unlabeled entry is the default (it is
// *written* as one — `pg@pin:` beside `pg/analytics@pin:` reads exactly that
// way); with no unlabeled entry, a sole labeled one is unambiguous and wins;
// several labeled entries and no default resolve to nothing, because picking
// one by sort order would send a call to whichever database's label happens
// to alphabetize first. Callers that must tell that refusal apart from "says
// nothing about pg" ask Ambiguous — Covers deliberately stays true for an
// ambiguous namespace, since the profile plainly does cover it.
func (p Profile) For(namespace string) (string, Connection, bool) {
	if key, conn, ok := p.ForInstance(namespace, ""); ok {
		return key, conn, ok
	}
	if labels := p.Instances(namespace); len(labels) == 1 {
		return p.ForInstance(namespace, labels[0])
	}
	return "", Connection{}, false
}

// Ambiguous reports whether a call naming this namespace without an instance
// has no single answer: several labeled entries and no unlabeled default.
func (p Profile) Ambiguous(namespace string) bool {
	labels := p.Instances(namespace)
	return len(labels) > 1 && labels[0] != ""
}

// Covers reports whether this profile says anything about a namespace.
func (p Profile) Covers(namespace string) bool {
	return len(p.Instances(namespace)) > 0
}

// DuplicateNamespaces reports which entries this profile names more than
// once — a stale pin left beside its replacement after a rebuild, most
// realistically. Two entries are duplicates when they share namespace *and*
// instance label: `pg@old` beside `pg@new` is the stale-pin accident, while
// `pg@pin` beside `pg/analytics@pin` is the multi-instance feature. Reported
// as the label a person would write ("pg", "pg/analytics") so a caller that
// must not act on an ambiguous answer — Lookup, and Check reporting the same
// thing it does — can refuse it as what it is, rather than the one entry
// that happened to sort first silently standing in for both.
func (p Profile) DuplicateNamespaces() []string {
	seen := map[string]int{}
	for _, key := range p.PluginKeys() {
		ns, instance, _ := SplitKey(key)
		id := ns
		if instance != "" {
			id = ns + "/" + instance
		}
		seen[id]++
	}
	var out []string
	for id, n := range seen {
		if n > 1 {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
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

// BadColor reports whether Color was written and is not a colour.
//
// The pattern is stated here rather than imported from internal/render/theme,
// which this package deliberately does not depend on — schema.go keeps its own
// copy of the same grammar for the same reason, and a drift test holds the
// three of them to one answer.
func (p Profile) BadColor() bool {
	return p.Color != "" && !colorPattern.MatchString(p.Color)
}

// BadTTL reports whether TTL was written and cannot be read.
func (p Profile) BadTTL() bool {
	if p.TTL == "" {
		return false
	}
	_, ok := p.Window()
	return !ok
}

// PluginNamespace is the namespace of a plugins: key, instance label and pin
// removed.
func PluginNamespace(key string) string {
	ns, _, _ := SplitKey(key)
	return ns
}

// SplitRef splits a profile reference — what --profile carries — into the
// profile name and the instance label: "staging" → (staging, ""),
// "staging/analytics" → (staging, analytics).
//
// One string rather than a second flag, deliberately. The reference already
// travels every surface unchanged — CLI flag, MCP call argument, grant
// record, audit line, TUI picker row — and "/" cannot appear in a profile
// name, so the parse is unambiguous everywhere at once. A second field would
// be a second column on grants, a second argument on the MCP schema, and a
// second thing every one of those surfaces must remember to print.
func SplitRef(ref string) (name, instance string) {
	name, instance, _ = strings.Cut(ref, "/")
	return name, instance
}

// RefName is the profile-name half of a reference. The bound surfaces
// compare with this: an active environment covers every instance grant
// inside it, and deleting a profile revokes them all.
func RefName(ref string) string {
	name, _ := SplitRef(ref)
	return name
}

// RefInstance is the instance half of a reference, "" for the default.
func RefInstance(ref string) string {
	_, instance := SplitRef(ref)
	return instance
}

// ValidRef reports whether ref is a legal profile reference: a valid name,
// optionally followed by one "/" and a valid instance label.
func ValidRef(ref string) bool {
	name, instance, cut := strings.Cut(ref, "/")
	if !ValidName(name) {
		return false
	}
	if !cut {
		return true
	}
	return ValidInstance(instance)
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

// KVUsers maps each kv store entry name onto the profiles whose connections
// read a credential from it, each list sorted.
//
// It exists so a listing of the store can say what an entry is *for*: the
// `secrets: password: kv:prod-db-password` line lives in this file, the entry
// lives in the store, and nothing joined the two — an operator staring at
// thirty entries could not see which ones their environments depend on and
// which are leftovers. Derived here rather than where it is displayed because
// SecretRefs owns the reference grammar, and a second walker in a display
// package is how the `kube:` scheme would get silently miscounted the day the
// grammar grows.
func (c Config) KVUsers() map[string][]string {
	out := map[string][]string{}
	for _, name := range c.ProfileNames() {
		p := c.Profiles[name]
		// One mention per profile, however many of its plugins share the
		// entry — the question is "who depends on this", not "how often".
		seen := map[string]bool{}
		for _, key := range p.PluginKeys() {
			for _, ref := range p.Plugins[key].SecretRefs() {
				if ref.Scheme != "kv" || seen[ref.Ref] {
					continue
				}
				seen[ref.Ref] = true
				out[ref.Ref] = append(out[ref.Ref], name)
			}
		}
	}
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
