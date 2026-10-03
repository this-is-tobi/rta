package http

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An SRE debugging a service on a port-forward, or on the internal network,
// has no way to ask for it: the guard was absolute at the terminal too, so the
// one REST client in the tool could reach everything but the thing being
// debugged. A person at the terminal may ask by name, per call. An agent never
// can: the input is Local, and the request is judged by what the surface may
// be given and not by what arrived.
func TestAPersonAtTheTerminalMayReachAServiceOfTheirOwn(t *testing.T) {
	useRealBlocklist(t)
	var hit bool
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		hit = true
		_, _ = w.Write([]byte("mine"))
	}))
	defer srv.Close()

	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceTUI} {
		hit = false
		if _, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}).WithSurface(sf)); err == nil || hit {
			t.Fatalf("%v: a loopback address was reached without being asked for", sf)
		}
		v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "local-network": true}).WithSurface(sf))
		if err != nil {
			t.Fatalf("%v: asked for by name, still refused: %v", sf, err)
		}
		if !hit || !strings.Contains(pairsOf(t, v)["body"], "mine") {
			t.Errorf("%v: the service was not reached", sf)
		}
	}

	hit = false
	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "local-network": true}).WithSurface(plugin.SurfaceMCP))
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.request.blocked" || hit {
		t.Errorf("over MCP the flag opened the way: %v (reached %v)", err, hit)
	}
}

// Asking for a service of one's own is not asking for the addresses cloud
// metadata lives at, which nothing of one's own is on.
func TestOwnNetworkNeverOpensTheMetadataAddresses(t *testing.T) {
	useRealBlocklist(t)
	for _, host := range []string{"169.254.169.254", "100.100.100.200", "[fe80::1]", "[fd00:ec2::254]", "240.0.0.1", "0.0.0.0"} {
		_, err := doRequest(context.Background(), "GET",
			req(map[string]any{"url": "http://" + host + ":9/", "local-network": true, "timeout": 1}).WithSurface(plugin.SurfaceCLI))
		blocked := err != nil && view.AsError(err, "x").Code == "http.request.blocked"
		if !blocked {
			t.Errorf("%s with local-network was not refused: %v", host, err)
		}
	}
}

// The refusal says what to ask for where asking works, and says there is no
// way round where there is none.
func TestTheBlockedHintOffersTheFlagOnlyWhereItWorks(t *testing.T) {
	useRealBlocklist(t)
	hint := func(url string) string {
		_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": url}).WithSurface(plugin.SurfaceCLI))
		return view.AsError(err, "x").Hint
	}
	if got := hint("http://127.0.0.1:9/"); !strings.Contains(got, "--local-network") {
		t.Errorf("a loopback address: hint %q, want the flag named", got)
	}
	if got := hint("http://169.254.169.254/"); strings.Contains(got, "--local-network") || !strings.Contains(got, "no way round") {
		t.Errorf("the metadata address: hint %q, want no flag and no way round", got)
	}
}
