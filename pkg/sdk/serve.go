package sdk

import (
	"context"
	"fmt"
	"os"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// Serve runs p as an rta plugin and does not return until the host
// disconnects. It is the whole of a plugin's main:
//
//	func main() { sdk.Serve(myplugin.Plugin()) }
//
// The declaration is validated before a single byte crosses the wire, and a
// plugin that fails validation exits non-zero with the reason on stderr
// rather than serving. This is the same plugin.Validate the built-in registry
// runs, deliberately: an author who gets a capability wrong should find out
// from `go run .`, in the first fifteen minutes, with the field named — not
// from a host that refuses to register them and cannot say which of their
// forty capabilities is at fault.
//
// It writes to stderr and calls os.Exit on failure, which is right for a main
// and wrong for anything else. ServeErr is the same thing for callers that
// want the error.
func Serve(p plugin.Plugin) {
	if err := ServeErr(p); err != nil {
		fmt.Fprintf(os.Stderr, "rta plugin %q: %v\n", p.Name, err)
		os.Exit(1)
	}
}

// ServeErr is Serve, returning instead of exiting. Tests and embedders use
// it; a plugin's main does not need it.
func ServeErr(p plugin.Plugin) error {
	if err := p.Validate(); err != nil {
		return err
	}
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         goplugin.PluginSet{PluginSetName: &grpcPlugin{decl: p}},
		GRPCServer:      goplugin.DefaultGRPCServer,
	})
	return nil
}

// grpcPlugin adapts a declaration to go-plugin's plugin interface.
//
// Only the server half is implemented here. GRPCClient is what the *host*
// would call to obtain a client stub, and the host does not use this type —
// internal/pluginhost dials PluginService directly, because it needs the raw
// connection to attach its own interceptors and to keep the whole client
// surface in one place it controls. Returning an error rather than a stub
// makes that explicit instead of leaving a second, subtly different client
// path for somebody to find and use later.
type grpcPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	decl plugin.Plugin
}

func (g *grpcPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	rtav1.RegisterPluginServiceServer(s, newServer(g.decl))
	return nil
}

func (g *grpcPlugin) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	return nil, fmt.Errorf("the host dials PluginService directly; see internal/pluginhost")
}
