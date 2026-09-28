package sdk

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/wire"
	"github.com/this-is-tobi/rta/pkg/view"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// A handler whose error is a *view.Error variable that stayed nil has
// succeeded, and the host is told so with the view it returned.
//
// Go makes that pointer a non-nil error the moment it is returned as one,
// and the path out read it as a failure: view.AsError unwrapped it to a nil
// *view.Error, which encoded as an Error with nothing in it, and the host
// printed "ERROR" with no code and no message for a call that had worked.
// qdrant.collection.list shipped that way, returning its helper's
// (view.Table, *view.Error) straight from Run.
func TestAHandlersNilViewErrorIsNoFailure(t *testing.T) {
	var none *view.Error
	table := view.Table{Columns: []view.Column{{Name: "name"}}, Rows: [][]string{{"docs"}}}
	p := plugin.Plugin{
		Name: "demo", Summary: "d",
		Capabilities: []plugin.Capability{{
			ID: "demo.list", Summary: "l", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return table, none },
			Prefill: func(context.Context, plugin.Request) (map[string]any, error) {
				return map[string]any{"name": "docs"}, none
			},
		}},
	}
	s := newServer(p)

	resp, err := s.Call(context.Background(), &rtav1.CallRequest{CapabilityId: "demo.list"})
	if err != nil {
		t.Fatal(err)
	}
	// Across the wire, since what the host reads is what the bytes say.
	resp = roundTrip(t, resp)
	if e := resp.GetError(); e != nil {
		t.Fatalf("a call that worked came back as a failure: %+v", wire.ErrorFromProto(e))
	}
	got, ok := wire.ViewFromProto(resp.GetView()).(view.Table)
	if !ok || len(got.Rows) != 1 || got.Rows[0][0] != "docs" {
		t.Errorf("the view did not come back: %#v", wire.ViewFromProto(resp.GetView()))
	}

	pre, err := s.Prefill(context.Background(), &rtav1.PrefillRequest{CapabilityId: "demo.list"})
	if err != nil {
		t.Fatal(err)
	}
	pre = roundTrip(t, pre)
	if e := pre.GetError(); e != nil {
		t.Fatalf("a prefill that worked came back as a failure: %+v", wire.ErrorFromProto(e))
	}
	if v := wire.ValuesFromProto(pre.GetValues())["name"]; v != "docs" {
		t.Errorf("the prefilled value did not come back: %v", v)
	}
}

// An error of the handler's own wrapping a nil *view.Error is the failure its
// words describe, and reaches the host coded and worded: read through to the
// nil pointer inside it, it crossed as an Error with nothing in it.
func TestAnErrorWrappingANilViewErrorIsItsWrappersFailure(t *testing.T) {
	var none *view.Error
	p := plugin.Plugin{
		Name: "demo", Summary: "d",
		Capabilities: []plugin.Capability{{
			ID: "demo.list", Summary: "l", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) {
				return nil, fmt.Errorf("listing collections: %w", none)
			},
		}},
	}
	resp, err := newServer(p).Call(context.Background(), &rtav1.CallRequest{CapabilityId: "demo.list"})
	if err != nil {
		t.Fatal(err)
	}
	got := wire.ErrorFromProto(roundTrip(t, resp).GetError())
	if got == nil || got.Code != "demo.list.failed" || !strings.HasPrefix(got.Message, "listing collections: ") {
		t.Errorf("the wrapper's failure crossed as %#v", got)
	}
}

func roundTrip[M proto.Message](t *testing.T, m M) M {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	out := m.ProtoReflect().New().Interface().(M)
	if err := proto.Unmarshal(raw, out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A handler in the plugin process is told the profile its call came through
// and the kind of forward the host opened on it, as a built-in is; and a
// host older than those fields, which sends neither, reads as a call no
// profile touched.
func TestAnExternalPluginsHandlerIsToldItsProfile(t *testing.T) {
	var profile string
	var tunnel plugin.Tunnel
	p := plugin.Plugin{
		Name: "demo", Summary: "d",
		Capabilities: []plugin.Capability{{
			ID: "demo.dump", Summary: "d", Safety: plugin.Read,
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				profile, tunnel = req.Profile(), req.Tunnel()
				return view.Text{}, nil
			},
		}},
	}
	s := newServer(p)
	for _, tc := range []struct {
		sent        *rtav1.CallRequest
		wantProfile string
		wantTunnel  plugin.Tunnel
	}{
		{&rtav1.CallRequest{CapabilityId: "demo.dump"}, "", plugin.TunnelNone},
		{&rtav1.CallRequest{CapabilityId: "demo.dump", Profile: "direct"}, "direct", plugin.TunnelNone},
		{&rtav1.CallRequest{CapabilityId: "demo.dump", Profile: "homelab", Tunnel: rtav1.Tunnel_TUNNEL_KUBE},
			"homelab", plugin.TunnelKube},
		{&rtav1.CallRequest{CapabilityId: "demo.dump", Profile: "bastion", Tunnel: rtav1.Tunnel_TUNNEL_SSH},
			"bastion", plugin.TunnelSSH},
		// A tunnel without a profile is none: the host opens a forward only on
		// a profile's connection.
		{&rtav1.CallRequest{CapabilityId: "demo.dump", Tunnel: rtav1.Tunnel_TUNNEL_KUBE}, "", plugin.TunnelNone},
	} {
		profile, tunnel = "unset", "unset"
		if _, err := s.Call(context.Background(), roundTrip(t, tc.sent)); err != nil {
			t.Fatal(err)
		}
		if profile != tc.wantProfile || tunnel != tc.wantTunnel {
			t.Errorf("sent %v: the handler saw profile %q, tunnel %q", tc.sent, profile, tunnel)
		}
	}
}
