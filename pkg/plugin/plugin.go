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
	// Local marks an input the host resolves from its own environment and
	// never accepts from a remote caller: the passphrase that unlocks a
	// store, not the payload going into it.
	//
	// Local fields are omitted from MCP tool schemas and stripped from
	// incoming MCP arguments. An agent must never be invited to supply,
	// invent, or repeat back a credential — one that reaches a model's
	// context has already leaked, whatever happens next. The operator
	// supplies it to the server instead (an environment variable set when
	// launching `rta mcp serve`). CLI and TUI offer Local fields normally:
	// there is a person there, and it is their credential.
	Local bool
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
	idRe      = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*){1,2}$`)
	nameRe    = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	fieldRe   = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	safetySet = map[Safety]bool{Read: true, Write: true, Destructive: true}
	// fieldTypes is the closed set an input may declare, in the order the
	// rejection message lists them.
	fieldTypes = []FieldType{String, Int, Bool, Float, StringSlice, Text, Path, Secret}
)

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
	if len(p.Capabilities) == 0 {
		return fmt.Errorf("plugin %q declares no capabilities", p.Name)
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
	if !safetySet[c.Safety] {
		return fmt.Errorf("capability %q: invalid safety %q", c.ID, c.Safety)
	}
	if c.Run == nil {
		return fmt.Errorf("capability %q: nil handler", c.ID)
	}
	scoped := c.Scope == ""
	for _, f := range c.Inputs {
		if !fieldRe.MatchString(f.Name) {
			return fmt.Errorf("capability %q: field name %q must be lowercase [a-z0-9-]", c.ID, f.Name)
		}
		if reservedInputs[f.Name] {
			return fmt.Errorf("capability %q: input %q is reserved by the host", c.ID, f.Name)
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

// reservedInputs are names the host injects into a request itself, so a
// capability may not also declare them.
//
// "detail" is set by every surface that owns the whole screen and cleared by
// Page for embedded sections. Without this check a plugin declaring an input
// called detail — an entirely natural name for "include per-item detail" —
// passed Validate and then panicked pflag with "flag redefined: detail" while
// the command tree was built, which kills every rta invocation including the
// doctor that would have diagnosed it.
var reservedInputs = map[string]bool{"detail": true}

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
