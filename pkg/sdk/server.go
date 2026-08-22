package sdk

import (
	"context"
	"fmt"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/wire"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
	rtav1 "github.com/this-is-tobi/rule-them-all/proto/rta/v1"
)

// server is the plugin side of PluginService: the four RPCs, each one a thin
// translation onto the handlers the author already wrote.
//
// It holds the declaration by capability ID rather than walking the slice per
// call. Not for speed — a plugin has tens of capabilities, not thousands —
// but because "the capability named in this request" is a lookup that either
// succeeds or is a protocol violation, and a map makes the second case one
// branch instead of a loop that falls off the end.
type server struct {
	rtav1.UnimplementedPluginServiceServer
	decl plugin.Plugin
	caps map[string]plugin.Capability
}

func newServer(p plugin.Plugin) *server {
	s := &server{decl: p, caps: make(map[string]plugin.Capability, len(p.Capabilities))}
	for _, c := range p.Capabilities {
		s.caps[c.ID] = c
	}
	return s
}

// capability resolves an ID the host named, or says why it could not.
//
// A miss is a gRPC error rather than a CallResponse carrying a view.Error,
// and that is the one place in this file where the distinction matters. The
// oneof exists for "the transport worked and the operation failed", which is
// an ordinary outcome a person should read. This is not that: the host is
// asking for a capability this plugin never declared, which means the host's
// registry and this process disagree about what this process is. That is a
// wire fault, and dressing it as an operation failure would put it in front
// of a user as though they had typed something wrong.
func (s *server) capability(id string) (plugin.Capability, error) {
	c, ok := s.caps[id]
	if !ok {
		return plugin.Capability{}, fmt.Errorf("no capability %q in plugin %q", id, s.decl.Name)
	}
	return c, nil
}

func (s *server) Describe(context.Context, *rtav1.DescribeRequest) (*rtav1.DescribeResponse, error) {
	return &rtav1.DescribeResponse{Plugin: wire.PluginToProto(s.decl)}, nil
}

// Call runs one capability.
//
// Every failure a handler can produce comes back inside CallResponse,
// including a panic. A plugin that panics mid-call would otherwise take its
// whole process down, and the process is shared by every other capability the
// plugin declares — so one bad row in one table would cost the user the nine
// tools they had open. The user sees a real error naming the capability; the
// process stays up; the next call works.
func (s *server) Call(ctx context.Context, req *rtav1.CallRequest) (resp *rtav1.CallResponse, err error) {
	c, err := s.capability(req.GetCapabilityId())
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			resp = &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{
				Error: wire.ErrorToProto(view.Errorf("plugin.panic",
					"%s crashed: %v", c.ID, r).
					WithHint("this is a bug in the plugin, not in what you asked for")),
			}}
			err = nil
		}
	}()

	pr := plugin.NewRequest(wire.ValuesFromProto(req.GetValues()), req.GetDryRun(), req.GetYes()).
		WithSurface(wire.SurfaceFromProto(req.GetSurface()))

	v, runErr := c.Run(ctx, pr)
	if runErr != nil {
		return &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{
			Error: wire.ErrorToProto(view.AsError(runErr, c.ID+".failed")),
		}}, nil
	}
	return &rtav1.CallResponse{Result: &rtav1.CallResponse_View{View: wire.ViewToProto(v)}}, nil
}

// Prefill returns current values for editing in place.
//
// A capability without a Prefill handler answers with no values rather than
// an error: the host only calls this when the declaration said has_prefill,
// so reaching here without one means the two sides disagree, and an empty
// form is a better outcome for the person in front of it than a failure.
func (s *server) Prefill(ctx context.Context, req *rtav1.PrefillRequest) (resp *rtav1.PrefillResponse, err error) {
	c, err := s.capability(req.GetCapabilityId())
	if err != nil {
		return nil, err
	}
	if c.Prefill == nil {
		return &rtav1.PrefillResponse{}, nil
	}
	defer func() {
		if r := recover(); r != nil {
			resp = &rtav1.PrefillResponse{Error: wire.ErrorToProto(view.Errorf("plugin.panic",
				"%s crashed while reading current values: %v", c.ID, r))}
			err = nil
		}
	}()

	values, prefillErr := c.Prefill(ctx, plugin.NewRequest(wire.ValuesFromProto(req.GetValues()), false, false))
	if prefillErr != nil {
		return &rtav1.PrefillResponse{Error: wire.ErrorToProto(view.AsError(prefillErr, c.ID+".prefill.failed"))}, nil
	}
	return &rtav1.PrefillResponse{Values: wire.ValuesToProto(values)}, nil
}

// Suggest completes one input from what exists right now.
//
// It cannot fail, by declaration: Suggest returns only []string, so a plugin
// that cannot reach its data source returns nothing and the surface falls
// back to free text. That is deliberate and it is why this RPC has no error
// field — a completion that pops an error dialog while somebody is typing is
// worse than a completion that does not appear.
func (s *server) Suggest(ctx context.Context, req *rtav1.SuggestRequest) (resp *rtav1.SuggestResponse, err error) {
	c, err := s.capability(req.GetCapabilityId())
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			resp, err = &rtav1.SuggestResponse{}, nil
		}
	}()
	for _, f := range c.Inputs {
		if f.Name != req.GetField() || f.Suggest == nil {
			continue
		}
		pr := plugin.NewRequest(wire.ValuesFromProto(req.GetValues()), false, false)
		return &rtav1.SuggestResponse{Values: f.Suggest(ctx, pr)}, nil
	}
	return &rtav1.SuggestResponse{}, nil
}
