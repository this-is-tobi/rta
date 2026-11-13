package sdk

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
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

	surface := wire.SurfaceFromProto(req.GetSurface())
	values, written, remove, err := writeFiles(wire.ValuesFromProto(req.GetValues()))
	defer remove()
	if err != nil {
		return &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{Error: wire.ErrorToProto(unwritable(c.ID, err))}}, nil
	}
	pr := plugin.NewRequest(values, req.GetDryRun(), req.GetYes()).
		WithSurface(surface).
		WithProfile(req.GetProfile(), wire.TunnelFromProto(req.GetTunnel())).
		WithTrusted(req.GetTrusted()).
		WithTemporary(written...)
	if surface == plugin.SurfaceMCP {
		pr = pr.WithConfinement(refuseDerived).WithLinkTargets(untoldTarget)
	}

	v, runErr := c.Run(ctx, pr)
	if runErr = plugin.Failure(runErr); runErr != nil {
		return &rtav1.CallResponse{Result: &rtav1.CallResponse_Error{
			Error: wire.ErrorToProto(plugin.HandlerFailure(runErr, c.ID, c.ID+".failed")),
		}}, nil
	}
	return &rtav1.CallResponse{Result: &rtav1.CallResponse_View{View: wire.ViewToProto(v)}}, nil
}

// writeFiles writes each value the host sent as the content of a file a
// setting names (plugin.FileContent) to a file of its own, and answers the
// values with each replaced by that file's path, the inputs it replaced, and
// the removal of what it wrote, which the caller defers whatever came of it.
//
// **In this process's own temporary directory, for the length of the call.**
// Under `confine: strict` that directory is the one place a plugin may write,
// private to its process, and its children inherit it with the rest of the
// sandbox: pg_dump reads the CA pg named for it. Removed once the call
// returns, so nothing of a CA an operator keeps in a cluster stays on disk past
// the call that needed it; a process killed in the middle leaves it in a
// directory the host removes whole when it reaps the process. Each file is
// named for its input, so a refusal naming the file names the setting too.
func writeFiles(values map[string]any) (map[string]any, []string, func(), error) {
	var names []string
	for name, v := range values {
		if _, ok := v.(plugin.FileContent); ok {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return values, nil, func() {}, nil
	}
	dir, err := os.MkdirTemp("", "rta-file-")
	if err != nil {
		return nil, nil, func() {}, err
	}
	remove := func() { _ = os.RemoveAll(dir) }
	out := maps.Clone(values)
	for _, name := range names {
		path := filepath.Join(dir, filepath.Base(name)+".pem")
		if err := os.WriteFile(path, values[name].(plugin.FileContent).Bytes(), 0o600); err != nil {
			return nil, nil, remove, err
		}
		out[name] = path
	}
	return out, names, remove, nil
}

// unwritable is the refusal of a call whose file could not be written.
func unwritable(id string, err error) *view.Error {
	return view.Errorf("plugin.file.unwritable", "%s could not write the file it was handed for this call: %v", id, err).
		WithHint("a plugin writes it in its own temporary directory, under TMPDIR: a disk that is full, or a " +
			"TMPDIR it cannot write to, stops it")
}

// refuseDerived is Request.Confine for a call an agent made: every path a
// handler worked out from the ones it was given is refused, the empty one
// aside, which names no path.
//
// The host judges each path an agent sends against the server's roots before
// the call crosses, and the roots do not cross with it, so nothing in this
// process can judge a path the handler reached from one — the repository a
// walk up from a directory finds, the file a config names. Confine let every
// such path through on the claim that the plugin's sandbox bounds it, and
// none does: on Linux a plugin runs unconfined, and on macOS its sandbox
// denies rta's own files and the credential directories and knows nothing of
// the roots. A handler that asks is told no, rather than answered from
// outside a root the operator drew; one that never asks is bounded by its
// sandbox and nothing else, which docs/95-reference/20-the-path-gate.md says.
func refuseDerived(field, path string) (string, *view.Error) {
	if strings.TrimSpace(path) == "" {
		return path, nil
	}
	return "", view.Errorf("core.mcp.path.derived",
		"%s: %q, which this call reached from the path it was given, cannot be checked against this "+
			"server's roots from a plugin", field, path).
		WithHint("a server judges only the paths a call names, and a plugin runs where its roots are not " +
			"known — name this path in the call itself, if an input takes one")
}

// untoldTarget is Request.LinkTarget for a call an agent made: no link's text,
// since whether it names a place outside the roots is the host's to say and
// the roots are not here (refuseDerived).
func untoldTarget(string, string) string {
	return "a path this plugin cannot check against the server's roots"
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

	given, written, remove, err := writeFiles(wire.ValuesFromProto(req.GetValues()))
	defer remove()
	if err != nil {
		return &rtav1.PrefillResponse{Error: wire.ErrorToProto(unwritable(c.ID, err))}, nil
	}
	values, prefillErr := c.Prefill(ctx, plugin.NewRequest(given, false, false).
		WithTrusted(req.GetTrusted()).WithTemporary(written...))
	if prefillErr = plugin.Failure(prefillErr); prefillErr != nil {
		return &rtav1.PrefillResponse{Error: wire.ErrorToProto(plugin.HandlerFailure(prefillErr, c.ID+"'s prefill", c.ID+".prefill.failed"))}, nil
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
		given, written, remove, err := writeFiles(wire.ValuesFromProto(req.GetValues()))
		defer remove()
		if err != nil {
			return &rtav1.SuggestResponse{}, nil
		}
		pr := plugin.NewRequest(given, false, false).
			WithSurface(wire.SurfaceFromProto(req.GetSurface())).
			WithTrusted(req.GetTrusted()).
			WithTemporary(written...)
		return &rtav1.SuggestResponse{Values: f.Suggest(ctx, pr)}, nil
	}
	return &rtav1.SuggestResponse{}, nil
}
