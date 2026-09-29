// Package registry holds every loaded capability — built-ins and (later)
// external plugins — and is the single source of truth all renderers and the
// MCP bridge read from.
package registry

import (
	"context"
	"fmt"
	"sort"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Origin is where a plugin came from: the binary on disk and the digest of
// its bytes, or the zero value for one compiled into rta.
//
// It lives here, beside the registration it describes, because that is the
// only arrangement in which it cannot disagree with what is registered.
// Before this it was a side map built from the plugin host's process cache
// and handed separately to the MCP gate, and the two fell out of step exactly
// once, which was enough: a plugin that stayed registered while dropping out
// of the host's bookkeeping was read by the gate as a built-in, and a
// built-in's authorization carries no digest to check. The artifact binding
// was defeated not by a flaw in the check but by the check asking a
// different component what it was looking at.
//
// It is set by the caller that did the loading and cannot be declared by a
// plugin: pkg/plugin has no field for it, because a value a plugin could
// state about its own provenance is a value a plugin could state falsely.
type Origin struct {
	Path   string
	Digest string
}

// External reports whether this came from a binary on $PATH rather than from
// the rta binary the operator already chose to run.
func (o Origin) External() bool { return o.Path != "" }

// Short is the digest abbreviated for display, and for the prefix match an
// operator's `plugins.<ns>@<digest>` pin is compared against.
func (o Origin) Short() string {
	if len(o.Digest) > 12 {
		return o.Digest[:12]
	}
	return o.Digest
}

// Artifact is the digest of the binary behind a namespace, empty for a
// built-in, and known=false for a namespace this registry has never heard
// of. It is what a grant records so its authority binds to a file rather
// than to a name — see grant.Grant.Digest — and it reads the same map the
// MCP gate compares against, so the two cannot disagree about what they are
// looking at.
func (r *Registry) Artifact(namespace string) (string, bool) {
	origin, known := r.Origin(namespace)
	if !known || !origin.External() {
		return "", known
	}
	return origin.Digest, true
}

// Registry indexes plugins and their capabilities by ID.
type Registry struct {
	plugins map[string]plugin.Plugin
	origins map[string]Origin
	caps    map[string]plugin.Capability
}

func New() *Registry {
	return &Registry{
		plugins: map[string]plugin.Plugin{},
		origins: map[string]Origin{},
		caps:    map[string]plugin.Capability{},
	}
}

// Register validates and adds a built-in plugin: one compiled into this
// binary, whose artifact is the rta the operator already chose to run.
func (r *Registry) Register(p plugin.Plugin) error {
	return r.RegisterFrom(p, Origin{})
}

// RegisterFrom adds a plugin loaded from somewhere, recording where.
// Namespaces are exclusive: two plugins cannot share one.
//
// Two entry points rather than one with a parameter every built-in would pass
// the zero value to, because the distinction is the point: a caller has to
// have an origin in hand to claim one, and `Register` reads as what it is.
func (r *Registry) RegisterFrom(p plugin.Plugin, origin Origin) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("registering plugin: %w", err)
	}
	if _, exists := r.plugins[p.Name]; exists {
		return fmt.Errorf("namespace %q already registered", p.Name)
	}
	// Every capability runs behind the host's check of what its inputs
	// declare — closed sets, numeric bounds — so no surface, nor a plugin's
	// own handler, has to remember to make it.
	// A copy of the slice, so the caller's declaration is left as it was.
	//
	// And each handler's error is read as the runtime reads it
	// (plugin.Failure), so a built-in returning a nil *view.Error as its
	// error has succeeded on every surface — each of them asks err != nil,
	// and the CLI went on to render the nil pointer it unwrapped.
	caps := make([]plugin.Capability, len(p.Capabilities))
	for i, c := range p.Capabilities {
		c.Run = settled(c.ID, plugin.GuardInputs(c))
		c.Prefill = settledPrefill(c.ID, c.Prefill)
		caps[i] = c
		r.caps[c.ID] = c
	}
	p.Capabilities = caps
	r.plugins[p.Name] = p
	r.origins[p.Name] = origin
	return nil
}

// settled is run, capability id's handler, with its error read through
// plugin.Failure and handed on as a surface can say it (worded).
func settled(id string, run plugin.Handler) plugin.Handler {
	if run == nil {
		return nil
	}
	return func(ctx context.Context, req plugin.Request) (view.View, error) {
		v, err := run(ctx, req)
		return v, worded(plugin.Failure(err), id, id+".failed")
	}
}

// settledPrefill is settled for a Prefill.
func settledPrefill(id string, prefill func(context.Context, plugin.Request) (map[string]any, error),
) func(context.Context, plugin.Request) (map[string]any, error) {
	if prefill == nil {
		return nil
	}
	return func(ctx context.Context, req plugin.Request) (map[string]any, error) {
		values, err := prefill(ctx, req)
		return values, worded(plugin.Failure(err), id+"'s prefill", id+".prefill.failed")
	}
}

// worded is a handler's failure of what as every surface reads it: err as it
// came when it has a code and words to say — a coded Error, or any other
// error, which a surface codes under its own fallback and words by its text
// — and otherwise coded under fallback and worded (plugin.HandlerFailure),
// as the plugin process's server sends one.
//
// A built-in's &view.Error{} it never filled in reached the CLI, the TUI and
// an agent as ERROR with no code and no message after it, where the same
// handler in a plugin's process was coded and worded before it left. err is
// kept as it came wherever it says something, so what a surface asks of it
// — a cancellation, a refusal's code — is still there to ask.
func worded(err error, what, fallback string) error {
	if err == nil {
		return nil
	}
	if verr := view.AsError(err, fallback); verr != nil && verr.Code != "" && verr.Message != "" {
		return err
	}
	return plugin.HandlerFailure(err, what, fallback)
}

// Origin reports where the named plugin came from, and whether that namespace
// is registered at all.
//
// The bool is not decoration: a gate that cannot tell "built in" from "never
// heard of it" is a gate that classifies an unknown namespace as the safer of
// the two, and the safer-looking one — built in — is the one that needs no
// digest pin.
func (r *Registry) Origin(namespace string) (Origin, bool) {
	o, ok := r.origins[namespace]
	return o, ok
}

// Capability returns the capability with the given ID.
func (r *Registry) Capability(id string) (plugin.Capability, bool) {
	c, ok := r.caps[id]
	return c, ok
}

// Plugins returns all registered plugins, sorted by name.
func (r *Registry) Plugins() []plugin.Plugin {
	out := make([]plugin.Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Capabilities returns all capabilities, sorted by ID.
func (r *Registry) Capabilities() []plugin.Capability {
	out := make([]plugin.Capability, 0, len(r.caps))
	for _, c := range r.caps {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
