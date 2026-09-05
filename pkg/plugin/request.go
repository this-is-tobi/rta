package plugin

import (
	"fmt"

	"github.com/this-is-tobi/rta/pkg/view"
)

// Request is what a handler runs with, and the accessors it reads inputs
// through. Built by plugin.Resolve on every surface, so a handler cannot
// tell a typed value from a defaulted one — which is the point.

// Surface names the renderer a request arrived through.
//
// Handlers must not branch on it to change what they do — one handler
// serving every surface is the point of the whole model,
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
	confine func(field, path string) (string, *view.Error)
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

// WithConfinement stamps the host's bound on what this call may open. A
// surface that confines paths calls it once, at the boundary, beside
// WithSurface.
func (r Request) WithConfinement(check func(field, path string) (string, *view.Error)) Request {
	r.confine = check
	return r
}

// Confine checks a path the handler *derived* rather than received, and
// returns the form it should open.
//
// The boundary can only check the strings a caller sent. A handler that turns
// one of them into a different path leaves that check behind, and nothing
// downstream knows the derivation happened: builtin/git receives a directory
// inside an allowed root and walks *upward* from it looking for the repository
// that directory belongs to, which is the right behaviour for a person in a
// subdirectory and an escape for an agent — root `~/work/project` with no
// repository in it, and a `.git` two levels up in `~`, means `git.diff`
// returns the contents of files the root was drawn to exclude. The handler is
// the only place that knows a second path exists, so it is the place that has
// to ask.
//
// An unconfined request — every surface with a person behind it, and every
// direct in-process caller — allows everything and returns the path unchanged,
// so a handler may call this unconditionally.
//
// Not carried across the plugin-host wire: an external plugin gets its bound
// from the sandbox it runs in, which is enforced by the operating
// system rather than by a promise the plugin makes.
func (r Request) Confine(field, path string) (string, *view.Error) {
	if r.confine == nil {
		return path, nil
	}
	return r.confine(field, path)
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
