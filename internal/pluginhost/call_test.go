package pluginhost

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	rtav1 "github.com/this-is-tobi/rta/proto/rta/v1"
)

// The plugin is told which profile a call came through and the kind of
// forward opened on it, for a receipt that names the connection again once
// the call is over — and nothing else about the profile: the values it filled
// travel as values, like any other layer's, and nothing names where they came
// from.
func TestACallCarriesItsProfileAndTunnelToThePlugin(t *testing.T) {
	for _, tc := range []struct {
		profile string
		tunnel  plugin.Tunnel
		want    rtav1.Tunnel
	}{
		{"", plugin.TunnelNone, rtav1.Tunnel_TUNNEL_UNSPECIFIED},
		{"direct", plugin.TunnelNone, rtav1.Tunnel_TUNNEL_UNSPECIFIED},
		{"homelab", plugin.TunnelKube, rtav1.Tunnel_TUNNEL_KUBE},
		{"bastion", plugin.TunnelSSH, rtav1.Tunnel_TUNNEL_SSH},
	} {
		req := plugin.ResolveRequest(plugin.Capability{ID: "db.status"}, plugin.Inputs{
			Caller: map[string]any{"host": "127.0.0.1"}, ProfileName: tc.profile, Tunnel: tc.tunnel,
		}, false, false).WithSurface(plugin.SurfaceCLI)
		got := callRequest("db.status", req)
		if got.GetProfile() != tc.profile || got.GetTunnel() != tc.want {
			t.Errorf("profile %q, tunnel %q: sent profile %q, tunnel %s", tc.profile, tc.tunnel,
				got.GetProfile(), got.GetTunnel())
		}
		if got.GetCapabilityId() != "db.status" || got.GetSurface() != rtav1.Surface_SURFACE_CLI ||
			got.GetValues()["host"].GetStringValue() != "127.0.0.1" {
			t.Errorf("the rest of the call went missing: %v", got)
		}
	}
}
