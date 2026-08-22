// Package registry holds every loaded capability — built-ins and (later)
// external plugins — and is the single source of truth all renderers and the
// MCP bridge read from.
package registry

import (
	"fmt"
	"sort"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
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
// built-in needs no digest pin on --allow-destructive. The artifact binding
// (ADR 0015, D27) was defeated not by a flaw in the check but by the check
// asking a different component what it was looking at.
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
// operator's --allow-destructive pin is compared against.
func (o Origin) Short() string {
	if len(o.Digest) > 12 {
		return o.Digest[:12]
	}
	return o.Digest
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
	r.plugins[p.Name] = p
	r.origins[p.Name] = origin
	for _, c := range p.Capabilities {
		r.caps[c.ID] = c
	}
	return nil
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
