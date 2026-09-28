package pluginhost

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/sdk/spelling"
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

// A plugin built on an older SDK answers a call whose handler returned a nil
// *view.Error with an Error that holds nothing, and the caller was shown
// ERROR and not one word more. It is named for what it is, and says what
// to do, in the words of its reader: the plugin's own upgrade at a terminal,
// and to an agent the operator's to run. Any other failure crosses as the
// plugin wrote it.
func TestAnEmptyFailureFromAPluginIsNamedForWhatItIs(t *testing.T) {
	for sf, want := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "— `rta plugin upgrade qdrant` moves it",
		plugin.SurfaceTUI: "— `rta plugin upgrade qdrant` moves it",
		plugin.SurfaceMCP: "— ask the operator to run `rta plugin upgrade qdrant`, which moves it",
	} {
		got := failure("qdrant.collection.list", sf, &rtav1.Error{})
		if got.Code != "plugin.error.empty" || !strings.Contains(got.Message, "qdrant.collection.list") ||
			!strings.Contains(got.Hint, want) {
			t.Errorf("over %s, an empty failure reads %+v", sf, got)
		}
		if hits := spelling.ForPlugin(plugin.Plugin{Name: "qdrant"}).Find(got.Hint, false); sf == plugin.SurfaceMCP && len(hits) > 0 {
			t.Errorf("the hint an agent reads spells a terminal's: %q", hits)
		}
	}
	written := &rtav1.Error{Code: "qdrant.conn.failed", Message: "could not reach", Hint: "is it up?", Retryable: true}
	if got := failure("qdrant.overview", plugin.SurfaceMCP, written); got.Code != "qdrant.conn.failed" || got.Message != "could not reach" ||
		got.Hint != "is it up?" || !got.Retryable {
		t.Errorf("a written failure crossed as %+v", got)
	}
	// A code alone, or a message alone, is still the plugin's own word.
	if got := failure("qdrant.overview", plugin.SurfaceCLI, &rtav1.Error{Code: "qdrant.x"}); got.Code != "qdrant.x" {
		t.Errorf("a coded failure with no message was replaced: %+v", got)
	}
}
