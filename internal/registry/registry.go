// Package registry holds every loaded capability — built-ins and (later)
// external plugins — and is the single source of truth all renderers and the
// MCP bridge read from.
package registry

import (
	"fmt"
	"sort"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// Registry indexes plugins and their capabilities by ID.
type Registry struct {
	plugins map[string]plugin.Plugin
	caps    map[string]plugin.Capability
}

func New() *Registry {
	return &Registry{
		plugins: map[string]plugin.Plugin{},
		caps:    map[string]plugin.Capability{},
	}
}

// Register validates and adds a plugin. Namespaces are exclusive: two plugins
// cannot share one.
func (r *Registry) Register(p plugin.Plugin) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("registering plugin: %w", err)
	}
	if _, exists := r.plugins[p.Name]; exists {
		return fmt.Errorf("namespace %q already registered", p.Name)
	}
	r.plugins[p.Name] = p
	for _, c := range p.Capabilities {
		r.caps[c.ID] = c
	}
	return nil
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
