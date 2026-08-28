package pluginhost

import (
	"context"
	"fmt"

	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/wire"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// noDispense satisfies go-plugin's PluginSet without providing a client.
//
// go-plugin requires a plugin set to negotiate the handshake, but its
// Dispense path returns an `any` the caller type-asserts — a shape that turns
// a protocol mismatch into a panic at the assertion rather than an error at
// the boundary. rta takes the raw *grpc.ClientConn off GRPCClient and builds
// its own typed stub, so this type exists only to be present and refuses to
// hand anything back.
type noDispense struct {
	goplugin.NetRPCUnsupportedPlugin
}

func (noDispense) GRPCServer(*goplugin.GRPCBroker, *grpc.Server) error { return nil }

func (noDispense) GRPCClient(context.Context, *goplugin.GRPCBroker, *grpc.ClientConn) (any, error) {
	// An error, not (nil, nil). go-plugin's Dispense hands its result back as
	// an `any` that the caller type-asserts, so a nil would surface as a
	// panic at some unrelated assertion rather than as a failure here. rta
	// never calls Dispense — it takes the raw connection off GRPCClient and
	// builds its own typed stub — and this says so at the one place somebody
	// would otherwise wire up a second, subtly different client path.
	return nil, fmt.Errorf("rta dials PluginService directly and does not dispense; see internal/pluginhost")
}

// call runs one capability in the plugin process.
//
// A transport failure and an operation failure are kept apart all the way to
// the caller. The plugin returns its own failures inside CallResponse, with a
// code and a hint a person can act on; a gRPC error means the process died,
// the deadline passed, or the two sides disagree about what exists. Folding
// them together would put "the plugin crashed" and "that host is not
// resolvable" in front of the user in the same words.
func (c *Client) call(ctx context.Context, id string, req plugin.Request) (view.View, error) {
	stub, err := c.live(ctx)
	if err != nil {
		return nil, view.Errorf("plugin.gone", "%s: %v", id, err)
	}
	resp, err := stub.Call(ctx, &rtav1.CallRequest{
		CapabilityId: id,
		Values:       wire.ValuesToProto(req.Values()),
		DryRun:       req.DryRun,
		Yes:          req.Yes,
		Surface:      wire.SurfaceToProto(req.Surface()),
	})
	if err != nil {
		return nil, c.transportError(ctx, id, err)
	}
	if e := resp.GetError(); e != nil {
		return nil, wire.ErrorFromProto(e)
	}
	return wire.ViewFromProto(resp.GetView()), nil
}

func (c *Client) prefill(ctx context.Context, id string, req plugin.Request) (map[string]any, error) {
	stub, err := c.live(ctx)
	if err != nil {
		return nil, view.Errorf("plugin.gone", "%s: %v", id, err)
	}
	resp, err := stub.Prefill(ctx, &rtav1.PrefillRequest{
		CapabilityId: id,
		Values:       wire.ValuesToProto(req.Values()),
	})
	if err != nil {
		return nil, c.transportError(ctx, id, err)
	}
	if e := resp.GetError(); e != nil {
		return nil, wire.ErrorFromProto(e)
	}
	return wire.ValuesFromProto(resp.GetValues()), nil
}

// suggest completes one input.
//
// Every failure is swallowed into "no suggestions", which is the contract
// Suggest already has for built-ins — it returns []string and nothing else.
// A dead plugin process while somebody is typing in a form should leave them
// typing, not raise a dialog over their input; the next real call will report
// the process is gone, with the context to explain it.
func (c *Client) suggest(ctx context.Context, id, field string, req plugin.Request) []string {
	// This does start the process if it is not running, and the first version
	// deliberately did not — on the reasoning that Suggest fires on a
	// keystroke and should never stall a shell for a handshake. That
	// reasoning was about a *crashed* plugin. Once declarations are cached,
	// "no process yet" became the ordinary state rather than the exceptional
	// one, so refusing to start meant an external plugin's completions never
	// worked until something else had happened to launch it — and, since the
	// stub is nil until then, it dereferenced one. The handshake is about
	// 40 ms, which a shell spends on tab without anybody noticing.
	//
	// Every failure still yields no suggestions rather than an error. That is
	// this RPC's whole contract: a completion that cannot answer must slow
	// nobody down, and one that pops a dialog over somebody's half-typed
	// command is worse than one that does not appear.
	stub, err := c.live(ctx)
	if err != nil {
		return nil
	}
	resp, err := stub.Suggest(ctx, &rtav1.SuggestRequest{
		CapabilityId: id,
		Field:        field,
		Values:       wire.ValuesToProto(req.Values()),
		// Carried across, so a plugin can tell a keystroke from a caller.
		// Without it every external plugin's Suggest saw SurfaceUnknown —
		// which pkg/plugin documents as an in-process caller inside the trust
		// boundary — and the rule that anything which would prompt or take a
		// visible moment must not run on this path had no way to be obeyed.
		Surface: wire.SurfaceToProto(req.Surface()),
	})
	if err != nil {
		return nil
	}
	return resp.GetValues()
}

// transportError turns a gRPC failure into something a person can read.
//
// Cancellation is reported as cancellation rather than as a plugin fault: a
// user pressing esc on a slow run, or a TUI abandoning a tile refresh, is the
// most common way this function is ever reached, and "the plugin failed" is
// the wrong thing to tell somebody about their own keystroke.
func (c *Client) transportError(ctx context.Context, id string, err error) *view.Error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return view.Errorf("plugin.canceled", "%s was stopped before it finished", id)
	}
	c.mu.Lock()
	exited := c.client != nil && c.client.Exited()
	c.mu.Unlock()
	if exited {
		return view.Errorf("plugin.gone", "the plugin serving %s stopped while it was running: %v", id, err).
			WithHint("it is restarted on the next call; this one is reported rather than retried, " +
				"because a call that died part-way may already have done what it was asked")
	}
	return view.Errorf("plugin.transport", "talking to the plugin serving %s: %v", id, err)
}
