package mcp

import (
	"context"
	"sync"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/toolcall"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// profileTools keeps the tools a profile can change in step with the file
// that states the profiles.
//
// A tool is registered once, from the profiles the server started with, and a
// client holds the list it was sent. A call is judged against the file as it
// is now (Options.Reload), so a profile written under a running server is
// required of a call at once while the list still says the tool takes no such
// argument: a client validating calls against its list could not send the one
// argument the refusal asked for, and the server advertised tools.listChanged
// and never sent it.
//
// Re-registering a tool is what makes the SDK send tools/list_changed, so the
// notification is a consequence of the list changing and cannot be sent for a
// list that did not. The file is read where a client is about to look at the
// list (tools/list) and where it is about to be refused for it (a tools/call of
// a tool a profile can change); there is no timer and no watcher, so nothing
// here outlives a request and a server that is never asked reads nothing.
//
// Only whether the tool takes the argument can move: the schema never lists
// the operator's connections (no enum), so a profile renamed, added beside
// another or removed from a namespace that keeps one leaves the list as it
// was. The handshake text is fixed when a client connects and is not
// rebuilt: a client that connects after the file gained its first profile gets
// the tool list that offers the argument and not the sentence that explains it.
type profileTools struct {
	server *sdk.Server
	reg    *registry.Registry
	opts   Options
	// caps are the offered tools a profile can change, by tool name.
	caps map[string]plugin.Capability

	mu sync.Mutex
	// published is whether the schema each of caps was last registered with
	// carries the argument, by capability ID.
	published map[string]bool
}

// followProfiles wires profileTools in front of the server's methods, and
// returns the middleware that does it.
func followProfiles(server *sdk.Server, reg *registry.Registry, opts Options, offered []plugin.Capability) sdk.Middleware {
	p := &profileTools{
		server: server, reg: reg, opts: opts,
		caps: map[string]plugin.Capability{}, published: map[string]bool{},
	}
	for _, c := range offered {
		if plugin.Profilable(c) {
			p.caps[plugin.ToolName(c.ID)] = c
			p.published[c.ID] = toolcall.TakesProfile(c, opts.Profiles.ProfilesFor(plugin.Namespace(c.ID)))
		}
	}
	return func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			switch method {
			case "tools/list":
				p.refresh()
			case "tools/call":
				if r, ok := req.(*sdk.CallToolRequest); ok && r.Params != nil {
					if _, watched := p.caps[r.Params.Name]; watched {
						p.refresh()
					}
				}
			}
			return next(ctx, method, req)
		}
	}
}

// refresh registers again every tool whose answer to "does it take a profile"
// is no longer the one it was registered under. A file that cannot be read
// says nothing about which tools have profiles, so it moves none.
func (p *profileTools) refresh() {
	live, known := p.opts.profileSet()
	if !known {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, c := range p.caps {
		takes := toolcall.TakesProfile(c, live.ProfilesFor(plugin.Namespace(c.ID)))
		if takes == p.published[c.ID] {
			continue
		}
		shown := p.opts
		shown.Profiles = live
		p.server.AddTool(toolDef(c, shown), handler(c, p.opts, p.reg))
		p.published[c.ID] = takes
	}
}
