// Package plugin defines the public contract every capability provider
// implements — built-ins and external plugins alike. There are no
// second-class plugins: a built-in declares itself exactly as an external
// one does, and the host cannot tell them apart. A Plugin is a namespace plus a set of Capabilities;
// a Capability is one operation with a stable ID, declared inputs, a safety
// class, and a handler returning a view.View.
package plugin

import (
	"context"
	"slices"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Safety classifies the blast radius of a capability. The host enforces
// confirmation and AI-exposure rules from it.
type Safety string

const (
	Read        Safety = "read"
	Write       Safety = "write"
	Destructive Safety = "destructive"
)

// FieldType enumerates supported input types. They map to CLI flags/args,
// JSON Schema properties, and MCP tool inputs.
//
// The set is closed, and the zero value is not a member: Validate rejects
// both an unrecognised type and an absent one, because every surface's switch
// has a default branch that would otherwise render either as a string and say
// nothing. Adding a constant here means adding it to fieldTypes — the test
// that walks this block by AST is what stops the two from drifting apart.
type FieldType string

const (
	String      FieldType = "string"
	Int         FieldType = "int"
	Bool        FieldType = "bool"
	Float       FieldType = "float"
	StringSlice FieldType = "stringSlice"
	// Text is a multiline string (markdown-friendly). CLI and MCP treat it
	// exactly like String; interactive surfaces offer a textarea instead of a
	// one-line input.
	Text FieldType = "text"
	// Path is a filesystem path on the machine running rta, and saying so is
	// what makes it completable. Every surface has a way to help with a path
	// and none of them can guess which inputs are one: the shell completes
	// files for it, the TUI completes directory by directory as you type, and
	// an MCP schema says whose filesystem it means — a model that reads
	// "file" alone has no reason to know the path is not its own.
	//
	// CLI and MCP carry it as a plain string; nothing is validated, because a
	// path that does not exist yet is the whole point of an output file.
	Path FieldType = "path"
	// Secret is a string that must never be echoed back — passphrases,
	// tokens. CLI and MCP treat it exactly like String (callers supply it via
	// a flag or env var, not a terminal prompt); the TUI masks it while
	// typing. Handlers should avoid putting a Secret value in a Text/Error
	// message, since that surfaces on every renderer.
	Secret FieldType = "secret"
	// SecretSlice is a repeatable Secret: a credential input a caller may
	// give more than once. `vault.kv.set --data 'password=hunter2'` is the
	// shape — the operation of writing a secret into a secret manager, whose
	// payload is a list of key=value pairs, every one of them the credential.
	//
	// It exists because there was no way to declare one, and "no way to
	// declare it" is not a gap an author routes around, it is one they fall
	// into: the only repeatable type was StringSlice, every sink keyed off
	// Secret, and so a credential supplied through such a field was written
	// verbatim to the completion shortlist (internal/recent) and into the
	// sealed agent log (internal/mcp's auditArgs) — which docs/22-audit-trail
	// promises holds "the arguments with secrets masked".
	//
	// It is Secret's sensitivity and StringSlice's shape, and both halves
	// have to be asked about separately: see Sensitive and Repeatable. CLI
	// carries it as a repeatable flag, MCP as an array of strings; the TUI
	// masks the box.
	//
	// Filled from an EnvFallback or a profile `secrets:` mapping it can only
	// ever carry one element — both deliver one string, which
	// Request.StringSlice reads as a one-item list. That is usable, and it is
	// not a list; an author who needs several values from the environment
	// wants several inputs.
	SecretSlice FieldType = "secretSlice"
)

// Sensitive reports whether a value of this type is a credential — the
// question every sink that must not write a caller's value down is actually
// asking, and the question `== Secret` only appeared to answer.
//
// A predicate rather than a comparison, because the comparison was the bug.
// The moment a second credential-carrying type existed, a dozen sites tested
// one member of a two-member set: internal/recent wrote a repeatable
// credential to the completion shortlist and offered it back on tab, and
// internal/mcp's auditArgs wrote one unmasked into the permanent, sealed
// agent log. Both key off this now, so a third member added later lands on
// every sink at once instead of on whichever ones somebody remembered.
func (t FieldType) Sensitive() bool { return t == Secret || t == SecretSlice }

// Repeatable reports whether a value of this type is a list rather than a
// scalar: a CLI flag that may be given more than once, a JSON Schema "array",
// a comma-separated box.
//
// The other half of the same split, and the half that fails quietly. Every
// surface switches on Field.Type with a default arm meaning "one string", so
// a SecretSlice reaching one becomes a single-valued flag that keeps the last
// value and drops the rest — no error, and the values that vanished were the
// credentials.
func (t FieldType) Repeatable() bool { return t == StringSlice || t == SecretSlice }

// EndpointRole is which part of a resolved tunnel an input takes.
//
// A tunnel produces exactly one fact — the local address a forward is
// listening on — and the roles are the shapes plugins take it in. They exist
// because the shapes genuinely differ rather than the names: a two-role
// Host/Port scheme was proposed and rejected for being unable to express `s3`,
// which has one input holding `host:port`, or `vault`, which has one holding a
// URL. These four cover every plugin in
// the tree, and a shape none of them fits is a reason to add a fifth rather
// than to make the operator hand-map it.
//
// The set is closed and the zero value is a member — EndpointNone, meaning
// "not filled from a tunnel", which is every input of every plugin that has
// not opted in.
type EndpointRole string

const (
	// EndpointNone is an input a tunnel never fills. The zero value.
	EndpointNone EndpointRole = ""
	// EndpointHost takes the address alone: "127.0.0.1".
	EndpointHost EndpointRole = "host"
	// EndpointPort takes the port alone, as an Int: 54321.
	EndpointPort EndpointRole = "port"
	// EndpointAddress takes both, joined: "127.0.0.1:54321".
	EndpointAddress EndpointRole = "address"
	// EndpointURL takes both as a URL: "http://127.0.0.1:54321".
	//
	// Plain http by default, and that is not a downgrade to argue about for
	// the ordinary case. A port-forward is already inside the API server's
	// TLS for the only hop that leaves the machine: rta talks to 127.0.0.1,
	// kubectl wraps that in HTTPS to the API server, and the kubelet
	// delivers it into the pod's network namespace unchanged. Re-encrypting
	// a loopback socket at each end buys nothing there.
	//
	// "Unchanged" is the part worth stating twice: a port-forward is a raw
	// byte pipe into the destination socket, not a proxy that terminates a
	// request. If that socket itself speaks TLS — a service's own listener,
	// as opposed to transport the forward provides — plain http is a
	// downgrade this default cannot see, because nothing about the forward
	// says so. config.Connection's `tunnelTLS: true` is how the operator says
	// it — named apart from any plugin's own tls/sslmode field, which is a
	// different fact at a different layer; see config.Connection.TunnelTLS's
	// doc comment. internal/profile's endpointValues makes the resulting
	// scheme choice.
	EndpointURL EndpointRole = "url"
	// EndpointTLS takes whether transport security is worth negotiating over
	// the forward, which is: no.
	//
	// **The host owns this decision, because only the host knows.** The
	// plugin cannot tell it is talking through a forward and rta cannot not
	// know. Left to the plugin's own default it actively breaks: PostgreSQL's
	// `prefer` kills the forward on the *clean disconnect*, measured against a
	// real cluster — the TLS layer's trailing close_notify arrives at a socket
	// PostgreSQL has already closed, the pod-side read resets, and kubectl
	// exits. The next call gets "connection refused" on a local port with
	// nothing anywhere connecting the two, and it happens on the first call,
	// to somebody who changed nothing.
	//
	// The value is rendered per input type: "disable" for a String whose
	// Options say so, false for a Bool. A caller who disagrees still wins,
	// because a caller-supplied value beats a tunnel.
	EndpointTLS EndpointRole = "tls"
)

// endpointRoles is every legal role, for validation and for the AST test that
// keeps this block and that list from drifting.
var endpointRoles = []EndpointRole{
	EndpointNone, EndpointHost, EndpointPort, EndpointAddress, EndpointURL, EndpointTLS,
}

// tlsOff is how a String input may spell "do not negotiate TLS", most
// preferred first.
//
// A list rather than one value because this is the plugin's vocabulary, not
// the host's: `pg` says `disable` because libpq does, and another library says
// `off` or `none`. The host has to produce a word the plugin will accept, and
// the only honest way to know which is to read what the author enumerated.
var tlsOff = []string{"disable", "off", "false", "none", "insecure"}

// TLSOffValue is the value the host fills into f to turn transport security
// off, and "" if f declares no way to say it.
//
// Empty is what validate refuses at registration, so by the time anything
// fills a tunnel this has an answer. Exported because the filler lives in
// internal/profile and this is the plugin contract's own statement about what
// a declaration means — deriving it a second time over there is how the two
// come to disagree about which word a plugin accepts.
func TLSOffValue(f Field) any {
	if f.Type == Bool {
		return false
	}
	if v := tlsOffValue(f); v != "" {
		return v
	}
	return nil
}

// tlsOffValue picks the first spelling f's Options offer.
func tlsOffValue(f Field) string {
	for _, want := range tlsOff {
		if slices.Contains(f.Options, want) {
			return want
		}
	}
	return ""
}

// Field declares one capability input.
type Field struct {
	Name string
	// Type is required: Validate rejects the zero value rather than treating
	// it as String. See Capability.validate for why an untyped input is a
	// security question and not only a tidiness one.
	Type       FieldType
	Help       string
	Default    any
	Required   bool
	Positional bool // rendered as a CLI positional argument instead of a flag
	// Local marks an input a remote caller may never supply: the passphrase
	// that unlocks a store, the path a revealed secret gets written to, the
	// address of the server a call is aimed at — not the payload going into
	// or out of it.
	//
	// Local fields are omitted from MCP tool schemas and stripped from
	// incoming MCP arguments, unconditionally. An agent must never be
	// invited to supply, invent, or repeat back a credential — one that
	// reaches a model's context has already leaked, whatever happens next —
	// and must never choose a destination for a value a grant only
	// authorized revealing, not redirecting. CLI and TUI offer Local fields
	// normally: there is a person there, and it is their machine.
	//
	// **A destination is a destination whether or not it is on this
	// machine**, and reading that narrowly was a live credential-redirect
	// hole. A service plugin declares the connection it
	// talks to — host, port, user, database, endpoint, region, address,
	// namespace — as ordinary inputs so config can fill them. Ordinary also
	// meant published in the MCP tool schema and accepted from a caller, and
	// plugin.Resolve applies caller values last, above config and above the
	// host's own environment. So an agent could name any server it liked and
	// the host would fill the operator's RTA_<NS>_PASSWORD in beside it —
	// pointing a real credential at a machine the agent chose. Marking those
	// inputs Local closes it with no contract change: config still fills
	// them, a person still passes them, and the one surface that must not
	// choose them no longer can.
	Local bool
	// EnvFallback lets a Local field also be resolved from this plugin's own
	// environment (RTA_<NAMESPACE>_<FIELD>) when nothing else supplied a
	// value — the passphrase or identity that unlocks a store, handed to an
	// unattended `rta mcp serve` the same way any other credential reaches a
	// long-running process. Ignored on a non-Local field.
	//
	// Off by default, and that default is the fix for a real bug: every
	// Local field used to get this for free, which is right for
	// a credential and wrong for a field that only chooses a destination —
	// kv.get's own --out is Local specifically so a grant on kv.get cannot
	// be read as "and write the value wherever you like", and an ambient
	// RTA_KV_OUT in the server's environment defeated that the same way an
	// explicit MCP argument would have, just more quietly. A field that only
	// picks a destination — where to write, which file to edit — should
	// require an explicit person at a terminal every time; a field that
	// authenticates should not have to be retyped on every call an operator
	// makes from the same shell. That distinction is a property of what the
	// field means, not of its FieldType, so it has to be declared rather
	// than inferred: kv.get's --out and kv.init's --identity are the same
	// FieldType (Path) and want opposite answers.
	EnvFallback bool
	// Config names a dotted key in the operator's configuration that this
	// input may be filled from when the caller supplied none, so somebody
	// states a connection once instead of retyping it on every invocation.
	//
	// Precedence is what the caller passed, then config, then Default. Your
	// handler reads req.String("host") either way and cannot tell which
	// happened, which is the point: a config-backed input is an ordinary
	// input, not a second way for a plugin to reach into the host.
	//
	// The key names a value inside your OWN section of that configuration.
	// Which section is yours is decided by the host from the artifact it
	// launched, not from the namespace you declare — a binary early on $PATH
	// can declare any namespace it likes, and an operator's stated values
	// must not be handed to whoever won that race (see internal/pluginconf).
	//
	// Refused on a Secret input. This path carries no credential: it is a
	// plaintext file, it is read on every invocation with nobody watching,
	// and a Secret filled from it would be published in an MCP tool schema
	// as a default. Declare Local instead and let the host resolve it from
	// its own environment, the way builtin/kv does. Also refused on a
	// Positional, because CLI positional arity is computed from Required and
	// a config-satisfied positional changes what "two arguments" means.
	Config string
	// Options enumerates every value this input accepts.
	//
	// Declared once, it becomes a real affordance on all four surfaces
	// rather than a sentence in the help text: a picker in the TUI form,
	// shell completion on the CLI, and an "enum" in the MCP schema — which
	// is the one that matters most, since a model guessing "PTR" at a field
	// that wants "ptr" currently learns so by failing.
	Options []string
	// Min and Max bound a numeric input, and are enforced by the host rather
	// than by each handler remembering to.
	//
	// A handler that reads req.Int("timeout") and multiplies gets whatever
	// the caller sent, including 0 and including negative — and the failure
	// is rarely a polite error. `net ping --timeout 0` reached
	// time.NewTicker(0) inside a library goroutine and took the process
	// down; over MCP that is one schema-valid call from an unprivileged
	// agent killing `rta mcp serve` for every other tool attached to it.
	//
	// Some handlers clamped by hand and some did not, which is the real
	// problem: "remember to clamp" is not a rule a third-party author can be
	// expected to follow, and there was nowhere to write the bound down. Now
	// there is, and it is enforced once, for every surface, in Resolve — and
	// published to MCP as JSON Schema minimum/maximum, so a model is told the
	// range instead of discovering it by crashing the server.
	//
	// Nil means unbounded in that direction. They apply to Int and Float.
	Min any
	Max any
	// Endpoint names which part of a resolved tunnel fills this input, so the
	// host can point a call at a port-forward it opened.
	//
	// A tunnel yields one thing — a local address — and plugins take it in
	// different shapes: `pg` wants `host` and `port` separately, `s3` wants one
	// `endpoint` holding `host:port`, `vault` wants one `address` holding a
	// URL. Declaring the role is how the host fills whichever of those a plugin
	// actually has, without knowing any of their names.
	//
	// **Declared rather than conventional**, because a plugin is free to call
	// its inputs `server` and `addr`, and matching on the names `host` and
	// `port` is the mistake this codebase has recorded four times: a name a
	// thing chooses for itself is not an identity.
	//
	// **Declared by the plugin rather than mapped by the operator**, which is
	// the opposite of the rule `secrets:` follows and for a reason that does
	// not apply here. The operator writes a secret mapping because a
	// plugin that could name the secret it wanted could name any secret in the
	// namespace — it would cause a *fetch*. This causes none: it names one of
	// the plugin's own declared inputs as the destination for a value the host
	// already computed, cannot point at anything else, and cannot read
	// anything. It is `Config` one step along — the plugin says which input a
	// value belongs in, and the host decides whether there is a value.
	//
	// Refused on a Secret input, for the reason Config is: an endpoint is not a
	// credential, and an input that takes one has no business being filled with
	// an address. At most one input may claim each role, and EndpointHost and
	// EndpointPort are declared together or not at all — half an address is not
	// a connection. Both are checked at registration, so a plugin that gets it
	// wrong fails to load rather than dialling somewhere surprising.
	//
	// Zero value is EndpointNone: no input is filled from a tunnel, which is
	// every plugin that has not opted in.
	Endpoint EndpointRole
	// TLSAdjacent marks an input that only affects the connection when this
	// capability's own EndpointTLS-role input actually negotiates TLS — a CA
	// bundle, a client certificate, a client key. It carries no Endpoint role
	// itself: the host does not fill it, the operator does. What it needs
	// said is the opposite fact — that a tunnel silently strips its effect,
	// because EndpointTLS is forced to its off value unconditionally
	// whenever a forward is open (see EndpointTLS), and a value that only
	// mattered once TLS was negotiated does nothing once TLS is not.
	//
	// **Not the same claim `checkSet` already makes about an Endpoint-role
	// input.** Those are overridden because the host writes into them
	// directly; this one is untouched and inert anyway, because what it
	// depended on was overridden instead — a harder failure to notice, since
	// nothing about the value itself looks wrong.
	//
	// **False by default, and that default is load-bearing, not merely
	// unset.** A plugin whose own connect() already compensates — turning
	// TLS back on when this field is given, the way plugins/etcd's
	// ca-file/cert-file/key-file do (`tls || ca-file != "" || cert-file !=
	// ""`) — must leave this false, or a connection that works correctly
	// gets refused for a reason that no longer applies to it. Only a plugin
	// that leaves the forced-off value standing, as plugins/pg's sslrootcert
	// does against its own multi-valued sslmode, opts in.
	TLSAdjacent bool
	// Suggest returns values that exist right now: the tags you have used,
	// the keys in your store, the hostnames in your hosts file. Unlike
	// Options it is not exhaustive — anything may still be typed — so it
	// helps without constraining.
	//
	// An entry may carry a tab-separated description ("3\tship the release"),
	// which shell completion shows and other surfaces strip.
	//
	// It runs on human surfaces only: shell completion and the TUI form. It
	// is never offered to an MCP caller, because the list itself is
	// information — the names of your secrets are worth something even
	// without their values — and an agent that legitimately needs it can
	// call the capability that lists them and be gated accordingly.
	//
	// It is called on a keystroke, so it must be cheap, side-effect free,
	// and silent on failure: return nil rather than an error, since a
	// completion that cannot answer should slow nobody down. req carries
	// what the caller has supplied so far, which is what lets a suggestion
	// depend on an earlier answer.
	//
	// With Live set the cadence changes and the rest stands: still
	// read-only, still silent on failure, but called only on a deliberate
	// completion press.
	Suggest func(ctx context.Context, req Request) []string
	// Live marks Suggest as reading the service this plugin fronts — a
	// bucket listing, a mount table — rather than computing locally.
	//
	// The split is the rule tunnels already follow: a read of somebody's
	// infrastructure must be something the operator did, not something
	// typing caused. A live Suggest runs only on a deliberate completion
	// press, and with the credentials the run would get (LiveRequest) —
	// the same pinned binary receives the same values moments earlier, on
	// a key that asked for exactly that. The per-keystroke channel never
	// calls it and carries no credentials: Candidates answers nil for a
	// live input.
	//
	// Entries may end in a separator ("backups/") to compose: an accepted
	// segment stops extending the box, so the next press fetches deeper
	// with the box's text as the partial — the field's own name carries it
	// in the request. Registration refuses Live without Suggest, beside
	// Options, or on anything but a String input.
	Live bool
}

// Handler executes a capability. Implementations must honor ctx cancellation.
type Handler func(ctx context.Context, req Request) (view.View, error)

// Capability is the atom of the system: one operation, one stable ID.
type Capability struct {
	// ID is dot-separated, 2 or 3 lowercase segments: "sys.cpu", "pg.table.list".
	// IDs are stable forever; input schemas evolve additively only.
	ID          string
	Summary     string
	Description string
	Safety      Safety
	Idempotent  bool
	Inputs      []Field
	Run         Handler
	// MinWidth is the narrowest column, in terminal cells, that this
	// capability's compact view stays useful in. 0 means it shrinks
	// gracefully and can go anywhere.
	//
	// It is a property of the content, not a layout instruction: gen.overview
	// shows a 44-character base64 key and a 36-character UUID, and in half a
	// column those are wrapped or truncated, which defeats the entire point
	// of showing a real value somebody can read and copy. Most capabilities
	// are summaries and shrink fine. A host with a grid gives a tile as many
	// columns as this needs and no more; a host without one ignores it.
	//
	// This used to be a map of capability IDs inside the TUI, which meant the
	// answer to "my plugin's tile is unreadable at half width" was to send a
	// patch to the host. Declaring it is the same fix available to everybody.
	MinWidth int

	// Detailed marks a capability with a richer full-page view. The host
	// sets the boolean "detail" request value when it has the whole screen
	// (a dashboard tile opened, a browse/search selection) and leaves it
	// false for compact previews (dashboard tiles). Handlers branch on
	// req.Bool("detail"). CLI exposes it as --detail; it is never a form
	// question. Capabilities without a richer view leave this false.
	Detailed bool
	// NoPreview keeps a capability off the automatic dashboard, however
	// cheap it looks from the outside.
	//
	// The dashboard fills itself with every capability that is Read and
	// needs no input, on the reasoning that such a capability is free to
	// run and therefore free to run every few seconds. That reasoning holds
	// for reading /proc: it costs nothing and tells nobody. It breaks in
	// two directions.
	//
	// Reaching off the box: disclosing a dependency list to a third party,
	// spending an API quota, waking a device. None of it is something a
	// person expects from opening a TUI. Network calls here happen only on
	// explicit user action.
	//
	// Unbounded local work: a recursive scan of wherever the caller
	// happened to be standing is cheap in a source tree and ruinous in a
	// home directory, and the dashboard would repeat it every few seconds
	// without anybody having asked once.
	//
	// Safety cannot express this: these capabilities really are Read, they
	// mutate nothing, and gating them behind a grant would be wrong. The
	// question is not whether the caller may run it, but whether the host
	// may run it *unasked*.
	//
	// It governs the automatic dashboard only. A person who names the
	// capability in their config has asked for it, and a search or browse
	// selection is a person asking right now.
	NoPreview bool
	// Prefill, when set, lets interactive surfaces edit-in-place: given the
	// required positional inputs (the record's identity), it returns current
	// values for the remaining fields — a todo edit form opens with today's
	// title and body, like editing an issue. Optional; CLI and MCP callers
	// pass explicit values and never need it.
	Prefill func(ctx context.Context, req Request) (map[string]any, error)
	// NeedsGrant marks a capability a remote caller may only invoke with a
	// grant a person issued for it (internal/grant). Destructive
	// capabilities need one implicitly, so this is for the ones the safety
	// class does not already catch: kv.get mutates nothing and is a leak.
	NeedsGrant bool
	// Scope names the input identifying the record this capability acts on —
	// "key" for kv.get, "id" for todo.rm. It lets a grant be narrowed to one
	// record instead of the whole capability, so "read the staging token"
	// does not have to mean "read every secret I own".
	Scope string
	// HostSpecific marks a capability whose answer is about the machine rta
	// happens to run on — its CPU, its filesystem, its checked-out repo, its
	// own hosts file — rather than a configured remote service or a pure
	// computation. Read only by the MCP bridge, to decide what a remote,
	// HTTP-transport server exposes.
	//
	// Defaults false, which is fail-*open*: the overwhelming majority of
	// capabilities reach somewhere the operator configured (pg.query,
	// kube.get) or compute from their own arguments (gen.password,
	// codec.b64), and neither changes meaning when the server does not run
	// where the caller is sitting. Defaulting the other way would block
	// almost everything remotely and put the burden of opting back in on
	// every plugin author; instead the small, identifiable set that
	// actually describes this machine opts in.
	//
	// A capability marked true is absent from tools/list entirely on a
	// remote-transport server — not merely refused per call — the same
	// registration-time treatment Safety already gets. There is no override
	// flag: an operator who wants sys.cpu from the box rta runs on has SSH.
	HostSpecific bool
}

// Plugin is a unit of distribution and a namespace.
type Plugin struct {
	Name         string // namespace: first segment of every capability ID
	Summary      string
	Version      string
	Capabilities []Capability
	// Needs is the credential locations this plugin cannot work without.
	//
	// Plugins run confined, and the confinement denies a standard list of
	// credential directories — a weather plugin has no business reading your
	// kubeconfig. But some plugins exist precisely to use one, and denying it
	// to them means shipping a plugin that cannot do the thing it is for:
	// `~/.kube` was on that list from the first commit of the plugin host,
	// which made both plugins/kube and plugins/cnpg unable to read a
	// kubeconfig at all on macOS.
	//
	// **Declaring a need does not grant it.** The declaration says what the
	// plugin is asking for; `rta plugin allow` is where somebody decides, and
	// the decision attaches to the artifact's digest exactly as trust does, so
	// a rebuild asks again. A plugin whose need is not granted still runs —
	// with the ordinary denials — and fails at the operation that wanted the
	// file, which is the honest outcome and the one that tells the operator
	// what to grant.
	Needs []Need
}

// Need names one credential location a plugin may ask to read.
//
// **A closed set, and deliberately not a path.** A plugin naming its own path
// would be a plugin choosing what to be allowed to read, which is the same
// mistake as letting a remote caller choose a destination — the operator would
// be approving a string rather than a thing they recognise. These name
// locations rta already knows about, one per entry of the deny list, so what
// `rta plugin allow` prints is a location an operator can reason about.
type Need string

const (
	NeedKubeconfig Need = "kubeconfig"      // ~/.kube
	NeedSSH        Need = "ssh"             // ~/.ssh
	NeedAWS        Need = "aws"             // ~/.aws
	NeedGnuPG      Need = "gnupg"           // ~/.gnupg
	NeedDocker     Need = "docker"          // ~/.docker/config.json
	NeedGCloud     Need = "gcloud"          // ~/.config/gcloud
	NeedNetrc      Need = "netrc"           // ~/.netrc
	NeedNPM        Need = "npmrc"           // ~/.npmrc
	NeedPyPI       Need = "pypirc"          // ~/.pypirc
	NeedGitCreds   Need = "git-credentials" // ~/.git-credentials
)

var needs = []Need{
	NeedKubeconfig, NeedSSH, NeedAWS, NeedGnuPG, NeedDocker,
	NeedGCloud, NeedNetrc, NeedNPM, NeedPyPI, NeedGitCreds,
}

// Needs returns every need, so anything mapping the set onto another
// representation — the wire codec, the deny list, the help text — has a finite
// list and a way to find out when it grows. The same reason EndpointRoles and
// FieldTypes are exported.
func Needs() []Need { return slices.Clone(needs) }

// KnownNeed reports whether n is a member.
func KnownNeed(n Need) bool { return slices.Contains(needs, n) }

// EndpointRoles returns every endpoint role, including EndpointNone.
//
// Exported for the same reason FieldTypes is: anything mapping a declaration
// onto another representation has a finite set to cover and a way to find out
// when it grows. The wire codec is the caller that matters — a role with no
// wire form crosses as "no tunnel", and the far side then runs the call
// against the plugin's own default host with nothing reporting why.
func EndpointRoles() []EndpointRole { return slices.Clone(endpointRoles) }

// Words returns the ID split into command segments, e.g. ["pg","table","list"].
func (c Capability) Words() []string { return strings.Split(c.ID, ".") }
