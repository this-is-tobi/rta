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
// bug. There are two legitimate uses. Trust: a request from SurfaceMCP has
// no human in the loop, so a capability whose blast radius is "an AI agent
// reads your secret" can require that an operator authorized it first. That
// is a question of *whether* the call is allowed, not of what it returns.
// And words: a hint that names a capability or an input names it the way its
// reader reaches it — a flag at a terminal, an argument to an agent, a box
// in a form — which is what the naming helpers beside this type spell from
// it (Surface.CapabilityName, InputName and the rest). That changes how a
// sentence spells a name, never what the call does.
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

// Tunnel is the kind of forward a call reached its server through, which the
// host opens on a profile's connection for that one call and closes when it
// ends (Request.Tunnel).
type Tunnel string

const (
	// TunnelNone is a call that reached its server directly: no profile, a
	// profile naming no coordinate, or one whose endpoint the caller named
	// themselves, which the host then connects to without a forward.
	TunnelNone Tunnel = ""
	// TunnelKube is a `kube:` coordinate's port-forward.
	TunnelKube Tunnel = "kube"
	// TunnelSSH is an `ssh:` coordinate's jump host.
	TunnelSSH Tunnel = "ssh"
)

// Request carries resolved inputs and invocation context to a handler.
type Request struct {
	values  map[string]any
	origins map[string]origin
	surface Surface
	profile string
	tunnel  Tunnel
	confine func(field, path string) (string, *view.Error)
	links   map[string]link
	targets func(dir, target string) string
	DryRun  bool
	Yes     bool
}

// link is a symbolic link a caller named, which the surface resolved before
// the handler saw the value (Request.Link).
type link struct{ path, target string }

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

// Profile is the operator's connection profile this call came through — the
// one --profile named, `rta use` switched on, or an agent's call carried —
// or "" for none.
//
// For the answer that names the connection again once the call is over. A
// dump taken through a `kube:` profile reached its server through a forward
// on 127.0.0.1 that closed when the call did, and a restore line naming the
// address it was handed named a port nothing listens on any more: the
// profile is what reaches the same server again, as
//
//	sf.Call("pg.restore", plugin.Arg{Name: "file", Value: out, Positional: true},
//		plugin.Arg{Name: "profile", Value: req.Profile()})
//
// with the profile given only when there is one. Given whenever there is,
// through a forward or not, since the credentials the call used may be the
// profile's and no other layer holds them; and the host and port beside it
// only when Tunnel is TunnelNone, since then they are the address the call
// reached — one the caller typed over the profile's, which a line naming the
// profile alone would send through the profile's forward instead. The values
// themselves are no guide to which case it is: a profile fills them the way
// config does, and a handler cannot tell which layer answered, which is the
// point (Resolve).
//
// A profile's name is the operator's configuration, not a secret, and it is
// the whole of what the host says about one — never its coordinate, which of
// the values it filled, or where its credentials come from.
func (r Request) Profile() string { return r.profile }

// Tunnel is the kind of forward the host opened on Profile's connection for
// this call, TunnelNone when it opened none. A handler that is about to name
// the address it connected to asks this first: through a tunnel that address
// is the host's end of a forward, gone once the call ends.
func (r Request) Tunnel() Tunnel { return r.tunnel }

// WithProfile stamps the profile a call came through and the tunnel opened on
// it. ResolveRequest stamps what its Inputs name, and the plugin process's
// server what the host sent; a tunnel without a profile is none, since the
// host opens a forward only on a profile's connection.
func (r Request) WithProfile(name string, t Tunnel) Request {
	if name == "" {
		t = TunnelNone
	}
	r.profile, r.tunnel = name, t
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

// WithLink records that the path the caller gave for field was a symbolic
// link at path holding target, which the surface resolved before handing
// the handler the value. A surface that substitutes what it judged for what
// it was given calls it at the boundary, beside WithConfinement.
func (r Request) WithLink(field, path, target string) Request {
	links := make(map[string]link, len(r.links)+1)
	for k, l := range r.links {
		links[k] = l
	}
	links[field] = link{path: path, target: target}
	r.links = links
	return r
}

// Link reports whether the path the caller gave for field was a symbolic
// link the surface resolved before the handler saw it: where the link is,
// and what it holds.
//
// For the handler whose answer is about the name rather than the file behind
// it. The MCP bridge hands a handler the path its guard judged, symlinks
// resolved (checkPaths says why), and that is the one to open; but a
// resolv.conf that is a link into /run is how systemd-resolved says it owns
// the file, and net.resolver.list, handed the file at the far end, told an
// agent "nothing — safe to edit" about a file the CLI rightly said gets
// overwritten. What was named is recorded here instead, for saying, never
// for opening: a link opened again leads wherever it points by then, not to
// what was judged.
//
// ok is false on a surface that hands the path over as it was given — the
// CLI and the TUI — where the handler can ask the filesystem itself, and on
// a plugin's side of the plugin-host wire, which does not carry it.
func (r Request) Link(field string) (path, target string, ok bool) {
	l, ok := r.links[field]
	return l.path, l.target, ok
}

// WithLinkTargets stamps how this surface tells what a symbolic link holds,
// for LinkTarget. A surface that confines paths calls it once, at the
// boundary, beside WithConfinement.
func (r Request) WithLinkTargets(tell func(dir, target string) string) Request {
	r.targets = tell
	return r
}

// LinkTarget is what a handler may say a symbolic link in dir holds, given
// target, the text os.Readlink read from it: that text, or a phrase that
// says the link leads outside what this caller may look at, without the
// name.
//
// For the handler that shows links it came across rather than ones it was
// given — fs.tree lists a directory's links with what each holds. Link says
// the same for a path the caller named, and the surface answers both by one
// rule: a link's text is a name, and one outside the roots is a name the
// caller may not read, whether or not the link leads back inside.
//
// An unconfined request — every surface with a person behind it, and every
// direct in-process caller — tells every target as it is, so a handler may
// call this unconditionally. Not carried across the plugin-host wire, like
// Confine.
func (r Request) LinkTarget(dir, target string) string {
	if r.targets == nil {
		return target
	}
	return r.targets(dir, target)
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
	// An overlaid value is the embedding page's, not the operator's, so it
	// loses the note of where the value it replaced came from.
	if len(r.origins) > 0 {
		kept := make(map[string]origin, len(r.origins))
		for k, o := range r.origins {
			if _, over := values[k]; !over {
				kept[k] = o
			}
		}
		r.origins = kept
	}
	// And the note of a link it named: the value is no longer that path.
	if len(r.links) > 0 {
		kept := make(map[string]link, len(r.links))
		for k, l := range r.links {
			if _, over := values[k]; !over {
				kept[k] = l
			}
		}
		r.links = kept
	}
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
	return stringSlice(r.values[name])
}

// stringSlice is StringSlice's reading of one value, for the readers that
// hold a value rather than a Request — the input guard among them, which has
// to see a list exactly as the handler will.
func stringSlice(v any) []string {
	switch v := v.(type) {
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
