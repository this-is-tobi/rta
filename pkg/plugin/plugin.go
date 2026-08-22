// Package plugin defines the public contract every capability provider
// implements — built-ins and external plugins alike (PROJECT.md P6: no
// second-class plugins). A Plugin is a namespace plus a set of Capabilities;
// a Capability is one operation with a stable ID, declared inputs, a safety
// class, and a handler returning a view.View.
package plugin

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Safety classifies the blast radius of a capability. The host enforces
// confirmation and AI-exposure rules from it (PROJECT.md §4.7).
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
)

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
	// hole (PROJECT.md D94). A service plugin declares the connection it
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
	// Off by default, and that default is the fix for a real bug (PROJECT.md
	// D74): every Local field used to get this for free, which is right for
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
	Suggest func(ctx context.Context, req Request) []string
}

// Surface names the renderer a request arrived through.
//
// Handlers must not branch on it to change what they do — one handler
// serving every surface is the point of the whole model (PROJECT.md P1),
// and a capability that behaves differently in the TUI than in a pipe is a
// bug. The one legitimate use is trust: a request from SurfaceMCP has no
// human in the loop, so a capability whose blast radius is "an AI agent
// reads your secret" can require that an operator authorized it first. That
// is a question of *whether* the call is allowed, not of what it returns.
//
// SurfaceUnknown means a direct in-process caller (tests, embedding code),
// which is inside the trust boundary. Every renderer that can be reached
// from outside the process must stamp its surface.
type Surface string

const (
	SurfaceUnknown Surface = ""
	SurfaceCLI     Surface = "cli"
	SurfaceTUI     Surface = "tui"
	SurfaceMCP     Surface = "mcp"
	// SurfaceCompletion is a keystroke rather than a caller: the shell asking
	// what could come next while somebody is still typing the command.
	//
	// It is its own surface because of what is *not* there. Nobody is waiting
	// to answer a question — a passphrase prompt fired by the tab key would
	// hang a shell mid-command-line on a question nobody expects — and the
	// only output that can be seen is a word list. So anything that would
	// prompt, confirm, or take a visible moment must not run here, and
	// checking the surface is how that stays true without every Suggest
	// having to remember it.
	SurfaceCompletion Surface = "completion"
)

// Request carries resolved inputs and invocation context to a handler.
type Request struct {
	values  map[string]any
	surface Surface
	DryRun  bool
	Yes     bool
}

// NewRequest builds a Request from resolved input values.
func NewRequest(values map[string]any, dryRun, yes bool) Request {
	if values == nil {
		values = map[string]any{}
	}
	return Request{values: values, DryRun: dryRun, Yes: yes}
}

// Surface reports which renderer this request came through.
func (r Request) Surface() Surface { return r.surface }

// WithSurface stamps the calling renderer on a request. Renderers call this
// once, at the boundary; handlers only ever read it.
func (r Request) WithSurface(s Surface) Request {
	r.surface = s
	return r
}

// With returns a copy of r carrying values overlaid on the inputs it already
// holds. It is how a composed detail page hands its own inputs down to the
// capabilities it embeds (see Page): a section built from kv.list needs the
// unlock key the page was given, and a section built from a per-host check
// needs the host. The receiver is unchanged, so a handler cannot reshape the
// request its caller is still holding.
func (r Request) With(values map[string]any) Request {
	merged := make(map[string]any, len(r.values)+len(values))
	for k, v := range r.values {
		merged[k] = v
	}
	for k, v := range values {
		merged[k] = v
	}
	r.values = merged
	return r
}

// Values returns every resolved input, as a copy.
//
// The typed accessors below are what a handler wants: they name one input and
// coerce it. This is for the one caller that cannot name them — the plugin
// host, which has to put the whole request on a wire without knowing what any
// of it means. A copy rather than the map itself, for the same reason With
// returns a new Request: a handler must not be able to reshape a request its
// caller is still holding, and neither must a transport.
func (r Request) Values() map[string]any {
	out := make(map[string]any, len(r.values))
	for k, v := range r.values {
		out[k] = v
	}
	return out
}

func (r Request) String(name string) string {
	v, _ := r.values[name].(string)
	return v
}

func (r Request) Int(name string) int {
	n, _ := toInt(r.values[name])
	return n
}

func (r Request) Bool(name string) bool {
	v, _ := r.values[name].(bool)
	return v
}

func (r Request) Float(name string) float64 {
	n, _ := toFloat(r.values[name])
	return n
}

// StringSlice reads a StringSlice input. A bare string is treated as one
// value rather than none: a caller passing a scalar where a list is declared
// almost always means "just this one" — --tag work is not a wordy way of
// saying no tags — and an MCP client is not schema-checked before its
// arguments reach here (the SDK's own contract makes validation the
// caller's responsibility), so a model that sends {"key": "x"} instead of
// {"key": ["x"]} is a case this has to get right, not just a style
// preference.
//
// This one behavior is now the single source of truth for what a scalar
// means in a list slot. It used to be answered twice — this accessor said
// "nothing", while internal/grant read the same raw value and said "exactly
// this one thing" — and the two answers did not agree. A per-key grant on
// kv.env, scoped to db-password, was satisfied by the string form of the
// call (the gate's reading), while the handler's nil (its own reading) took
// the "no keys named" branch and exported the entire store. Two readers of
// one untyped value must not be free to disagree about what it means.
func (r Request) StringSlice(name string) []string {
	switch v := r.values[name].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			out = append(out, fmt.Sprint(e))
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	}
	return nil
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
	// person expects from opening a TUI, and D9 promises network calls
	// happen only on explicit user action.
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
}

// Plugin is a unit of distribution and a namespace.
type Plugin struct {
	Name         string // namespace: first segment of every capability ID
	Summary      string
	Version      string
	Capabilities []Capability
}

var (
	idRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*){1,2}$`)
	nameRe  = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	fieldRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// A dotted path of the same segments, so a plugin can nest its own
	// settings. Closed deliberately: no leading dot, no empty segment, no
	// "..", nothing that could be read as a filesystem path by whatever
	// looks at it next.
	configKeyRe = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)
	safetySet   = map[Safety]bool{Read: true, Write: true, Destructive: true}
	// safeties is safetySet in the order harm increases, which is the order
	// anything listing them should use.
	safeties = []Safety{Read, Write, Destructive}
	// fieldTypes is the closed set an input may declare, in the order the
	// rejection message lists them.
	fieldTypes = []FieldType{String, Int, Bool, Float, StringSlice, Text, Path, Secret}
)

// FieldTypes returns every type an input may declare, in the order the
// rejection message lists them.
//
// The set is closed and the zero value is not a member (ADR 0011), so anything
// that maps a declaration onto another representation — a wire enum, a JSON
// Schema, a form widget, a code generator — has a finite set to cover and a
// way to find out when it grows. Exported for exactly that: the alternative is
// every such mapping restating the list from memory, which is how one of them
// ends up missing Secret and rendering a credential as a plain string field.
func FieldTypes() []FieldType { return slices.Clone(fieldTypes) }

// Safeties returns every safety class, in the order harm increases.
func Safeties() []Safety { return slices.Clone(safeties) }

// fieldTypeList renders the accepted types for an error message, so a rejected
// plugin is told what to write instead of what not to.
func fieldTypeList() string {
	names := make([]string, len(fieldTypes))
	for i, t := range fieldTypes {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

// Validate checks structural correctness of a plugin declaration. It is used
// by the registry at load time and by the sdktest conformance suite.
func (p Plugin) Validate() error {
	if !nameRe.MatchString(p.Name) {
		return fmt.Errorf("plugin name %q: must be lowercase [a-z0-9-]", p.Name)
	}
	if why, reserved := reservedNamespaces[p.Name]; reserved {
		return fmt.Errorf("plugin name %q is reserved by the host (%s); pick another namespace",
			p.Name, why)
	}
	if len(p.Capabilities) == 0 {
		return fmt.Errorf("plugin %q declares no capabilities", p.Name)
	}
	// Name is already constrained to [a-z0-9-] by nameRe; the prose is not.
	if err := checkLine(fmt.Sprintf("plugin %q summary", p.Name), p.Summary, maxSummary); err != nil {
		return err
	}
	if err := checkLine(fmt.Sprintf("plugin %q version", p.Name), p.Version, maxOption); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, c := range p.Capabilities {
		if err := c.validate(p.Name); err != nil {
			return err
		}
		if seen[c.ID] {
			return fmt.Errorf("duplicate capability ID %q", c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}

func (c Capability) validate(ns string) error {
	if !idRe.MatchString(c.ID) {
		return fmt.Errorf("capability ID %q: want 2-3 lowercase dot-separated segments", c.ID)
	}
	if !strings.HasPrefix(c.ID, ns+".") {
		return fmt.Errorf("capability %q: ID must start with plugin namespace %q", c.ID, ns)
	}
	if c.Summary == "" {
		return fmt.Errorf("capability %q: summary is required", c.ID)
	}
	if err := checkLine(fmt.Sprintf("capability %q: summary", c.ID), c.Summary, maxSummary); err != nil {
		return err
	}
	if err := checkText(fmt.Sprintf("capability %q: description", c.ID), c.Description, maxDescription); err != nil {
		return err
	}
	if !safetySet[c.Safety] {
		return fmt.Errorf("capability %q: invalid safety %q", c.ID, c.Safety)
	}
	if c.Run == nil {
		return fmt.Errorf("capability %q: nil handler", c.ID)
	}
	scoped := c.Scope == ""
	seenInputs := map[string]bool{}
	for _, f := range c.Inputs {
		if !fieldRe.MatchString(f.Name) {
			return fmt.Errorf("capability %q: field name %q must be lowercase [a-z0-9-]", c.ID, f.Name)
		}
		// Two fields sharing a name is not a harmless typo: declareFlags
		// registers one pflag.Flag per input in declaration order, and the
		// second AddFlag for the same name panics the whole process at
		// startup — not just for the capability that declared it, since
		// every registered plugin's flags are built into one command tree
		// before any command runs. The same duplicate-check Plugin.Validate
		// already does one level up, for capability IDs.
		if seenInputs[f.Name] {
			return fmt.Errorf("capability %q declares input %q twice", c.ID, f.Name)
		}
		seenInputs[f.Name] = true
		if why, reserved := reservedInputs[f.Name]; reserved {
			return fmt.Errorf("capability %q: input %q is reserved by the host (%s); rename it",
				c.ID, f.Name, why)
		}
		if f.Config != "" {
			if !configKeyRe.MatchString(f.Config) {
				return fmt.Errorf("capability %q: input %q has config key %q; want dot-separated lowercase segments",
					c.ID, f.Name, f.Config)
			}
			// Refused rather than quietly ignored, and the message names the
			// alternative, because an author who reaches for this is trying
			// to solve a real problem and needs to be pointed at the path
			// that already solves it.
			if f.Type == Secret {
				return fmt.Errorf("capability %q: input %q is a Secret and cannot be filled from config; "+
					"config is a plaintext file read on every invocation and a Secret default is published "+
					"in the MCP tool schema — declare Local: true and let the host resolve it from its own "+
					"environment instead", c.ID, f.Name)
			}
			if f.Positional {
				return fmt.Errorf("capability %q: input %q is positional and cannot be filled from config; "+
					"CLI argument arity is computed from Required, so a config-satisfied positional changes "+
					"what an argument count means", c.ID, f.Name)
			}
		}
		if f.EnvFallback && !f.Local {
			return fmt.Errorf("capability %q: input %q declares EnvFallback without Local; "+
				"EnvFallback only changes how a Local field resolves, and a non-Local field is already "+
				"reachable from a caller directly", c.ID, f.Name)
		}
		// Every surface switches on Field.Type with a default branch meaning
		// "string", so a type nothing recognises is caught nowhere downstream.
		// Type: "integer" — JSON Schema's spelling, and the obvious thing to
		// reach for — builds a --port string flag, publishes {"type": "string"}
		// in the MCP schema, and hands the handler a value req.Int reads as 0.
		// No error anywhere: the capability just runs against port 0.
		//
		// It has to be rejected here rather than added later. Once pkg/plugin
		// is semver-committed, turning the silent string default into an error
		// breaks every plugin that had come to depend on it.
		//
		// The zero value is rejected too, and that is a deliberate second
		// decision rather than a consequence of the first. Field{Name: "host"}
		// used to validate and behave as a string everywhere, which is exactly
		// how a credential input ends up untyped: Secret is what makes a value
		// masked, Path is what makes it completable, and neither is something
		// the host can infer from a name. Making the author say which one it
		// is costs a word and is the only moment the question gets asked.
		switch {
		case f.Type == "":
			return fmt.Errorf("capability %q: input %q declares no type, want one of %s",
				c.ID, f.Name, fieldTypeList())
		case !slices.Contains(fieldTypes, f.Type):
			return fmt.Errorf("capability %q: input %q has unknown type %q, want one of %s",
				c.ID, f.Name, f.Type, fieldTypeList())
		}
		if err := checkText(fmt.Sprintf("capability %q: input %q help", c.ID, f.Name), f.Help, maxHelp); err != nil {
			return err
		}
		if err := checkBounds(c.ID, f); err != nil {
			return err
		}
		for _, o := range f.Options {
			// Options are published as an MCP enum and drawn as a select, so
			// they are as much displayed text as Help is.
			if err := checkLine(fmt.Sprintf("capability %q: input %q option %q", c.ID, f.Name, o), o, maxOption); err != nil {
				return err
			}
		}
		// A Default is printed in `--help`, seeded into every form, and
		// published in the MCP tool schema, so it is displayed text wherever
		// it came from. Both shapes are checked: only the string case was,
		// and a StringSlice input's []string default went to models verbatim
		// — the one declared-text hole left in a function whose whole job is
		// that there are none (ADR 0013).
		switch d := f.Default.(type) {
		case string:
			if err := checkLine(fmt.Sprintf("capability %q: input %q default", c.ID, f.Name), d, maxHelp); err != nil {
				return err
			}
		case []string:
			for i, e := range d {
				if err := checkLine(fmt.Sprintf("capability %q: input %q default[%d]", c.ID, f.Name, i), e, maxHelp); err != nil {
					return err
				}
			}
		}
		if f.Name == c.Scope {
			scoped = true
		}
	}
	// A Scope naming no input is not a harmless typo: grants would silently
	// stop narrowing to a record and start covering the whole capability.
	if !scoped {
		return fmt.Errorf("capability %q: scope %q names no input", c.ID, c.Scope)
	}
	return nil
}

// checkBounds rejects a Min/Max the host will never enforce.
//
// The field exists because "remember to clamp" is not a rule a third-party
// author can be expected to follow — `net ping --timeout 0` reached
// time.NewTicker(0) inside a library goroutine and took the process down, and
// over MCP that is one schema-valid call from an unprivileged agent killing
// `rta mcp serve` for every tool attached to it. A bound that is declared and
// silently not applied is worse than no bound at all: the author believes the
// input is clamped and stops checking, and nothing anywhere says otherwise.
//
// Three ways to declare one that does nothing, all of them quiet:
//
//   - A non-numeric value. Resolve reads Min through toInt/toFloat, which
//     return not-ok for a string, so `Min: "1"` means "no minimum" — and the
//     MCP bridge publishes it as the JSON Schema "minimum" keyword regardless,
//     where a string is not a legal value, so the tool schema every connected
//     agent reads is malformed as well.
//   - A bound on a type Resolve does not clamp. Only Int and Float are
//     clamped, so a Min on a string is a promise nothing made.
//   - Min above Max. Clamping applies Min and then Max, so an inverted pair
//     does not error; it pins every value, including valid ones, to Max.
//
// All three were conformance-suite findings, which meant a plugin could fail
// `sdktest` and still register and run. They belong here instead: this is the
// gate every surface goes through, and the suite reports what this returns.
func checkBounds(id string, f Field) error {
	if f.Min == nil && f.Max == nil {
		return nil
	}
	if f.Type != Int && f.Type != Float {
		return fmt.Errorf("capability %q: input %q is %s and declares Min/Max, which apply only to %s and %s",
			id, f.Name, f.Type, Int, Float)
	}
	lo, loOK := toFloat(f.Min)
	hi, hiOK := toFloat(f.Max)
	if f.Min != nil && !loOK {
		return fmt.Errorf("capability %q: input %q has a non-numeric Min (%#v), which is not a bound the host can apply",
			id, f.Name, f.Min)
	}
	if f.Max != nil && !hiOK {
		return fmt.Errorf("capability %q: input %q has a non-numeric Max (%#v), which is not a bound the host can apply",
			id, f.Name, f.Max)
	}
	if loOK && hiOK && lo > hi {
		return fmt.Errorf("capability %q: input %q has Min %v above Max %v, so every value clamps to Max",
			id, f.Name, f.Min, f.Max)
	}
	return nil
}

// reservedInputs are names the host owns on a capability's command line, so a
// capability may not also declare them. Each carries why, because a rule with
// no stated reason is one nobody can check and nobody dares extend.
//
// The failure mode is silence, not a collision. cobra resolves a subcommand's
// own flag before an inherited one, so an input named "dry-run" does not
// conflict with the root's persistent --dry-run: it quietly *becomes* it, and
// the host's copy is never set. `rta acme wipe --yes --dry-run` against such a
// plugin exits 0, reports success, and performs the wipe — the operator asked
// for a preview and the one flag that promised nothing would happen is the
// flag that stopped working.
//
// This map cannot derive the CLI's flag set: it lives in the SDK, which knows
// nothing about cobra or internal/app. So it is the *contract* — the host
// declares what it reserves, and internal/app is tested to stay inside it by
// TestTheCLIReservesEveryNameItOwns. That test is the only thing keeping the
// two in step, which is exactly why this map had one entry while the CLI had
// grown five more names.
//
// Single letters are deliberately absent: an input named "o" registers the
// long flag --o, and pflag keeps long names and shorthands in separate
// namespaces, so -o still reaches --output. Verified, not assumed —
// over-reserving would refuse legitimate names for a collision that does not
// happen.
var reservedInputs = map[string]string{
	"detail": "the host sets it on any surface that owns the whole screen, and Page clears it for embedded sections",
	"dry-run": "the host's promise that a write or destructive capability changed nothing; " +
		"an input of this name makes that promise unkeepable",
	"yes":      "the host's record that a human confirmed a destructive operation",
	"output":   "chooses the renderer, so shadowing it means a caller cannot ask for JSON",
	"no-color": "disables styling, which is what makes rta's output safe to pipe",
	"help":     "cobra's; shadowing it makes `rta <ns> <cap> --help` unreachable",
}

// ReservedInputs lists the names the host owns, sorted.
//
// Exported so the CLI can be tested against it rather than expected to
// remember it: the flag set and this list live in different packages and
// neither derives from the other, so nothing but a test can hold them
// together.
func ReservedInputs() []string {
	out := make([]string, 0, len(reservedInputs))
	for name := range reservedInputs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// reservedNamespaces are rta's own top-level commands, so a plugin may not
// take one as its namespace.
//
// The same defect as reservedInputs, one level up, and it fails the same way:
// a namespace becomes a top-level command, so a plugin called "doctor"
// *replaces* `rta doctor` — which then prints the plugin's usage and exits 0,
// having run none of the checks an operator ran it for. The check most likely
// to reveal a hostile plugin is the one a hostile plugin can switch off, and
// nothing anywhere says it happened.
//
// RegisterFrom already refuses a namespace another *plugin* holds, which is
// why sys and kv cannot be taken. rta's own commands are not plugins and were
// protected by nothing.
//
// "help" and "completion" are cobra's rather than rta's; they are reserved
// for the same reason and kept in the same list, because the operator does
// not care whose command it is, only that it stopped working.
var reservedNamespaces = map[string]string{
	"completion": "generates the shell completion script",
	"doctor":     "diagnoses the installation, plugins included — the one command that must not be maskable",
	"explain":    "prints what a capability takes and what it is allowed to do",
	"help":       "cobra's help command",
	"init":       "writes a starter configuration",
	"mcp":        "serves and installs the MCP server",
	"plugin":     "lists, installs and scaffolds plugins",
}

// ReservedNamespaces lists the names rta's own commands hold, sorted. Exported
// for the same reason as ReservedInputs: the command tree lives in
// internal/app and this list lives here, so only a test can hold them
// together — TestTheCLIReservesEveryTopLevelCommandItOwns.
func ReservedNamespaces() []string {
	out := make([]string, 0, len(reservedNamespaces))
	for name := range reservedNamespaces {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// Words returns the ID split into command segments, e.g. ["pg","table","list"].
func (c Capability) Words() []string { return strings.Split(c.ID, ".") }

// Candidates returns what a human surface may offer for this input:
// the closed set when there is one, otherwise whatever exists right now.
// Entries may carry a tab-separated description (see Field.Suggest).
//
// Options wins over Suggest: a field that declares both is saying "these are
// the values, and here are some of them", which the closed set already
// answers.
func (f Field) Candidates(ctx context.Context, req Request) []string {
	if len(f.Options) > 0 {
		return f.Options
	}
	if f.Suggest == nil {
		return nil
	}
	return f.Suggest(ctx, req)
}

// CandidateValue drops the description from a completion entry, leaving the
// value a caller would actually submit.
func CandidateValue(entry string) string {
	if i := strings.IndexByte(entry, '\t'); i >= 0 {
		return entry[:i]
	}
	return entry
}
