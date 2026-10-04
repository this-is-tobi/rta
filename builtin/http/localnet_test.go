package http

import (
	"context"
	stdnet "net"
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

// ::1 is the IPv6 loopback and nothing else: the IPv4-compatible reading that
// takes the last 32 bits of an address in ::/96 for an IPv4 one made it 0.0.0.1,
// which is in "this network" and is refused, so the flag that allows a loopback
// address refused the loopback address `localhost` resolves to on most machines.
func TestOwnNetworkAllowsExactlyTheIPv6Loopback(t *testing.T) {
	for ip, want := range map[string]struct{ blocked, own string }{
		"::1":                {blocked: "a loopback address", own: ""},
		"127.0.0.1":          {blocked: "a loopback address", own: ""},
		"::":                 {blocked: "the unspecified address", own: "the unspecified address"},
		"::2":                {blocked: "IPv4-compatible", own: "IPv4-compatible"},
		"::a9fe:a9fe":        {blocked: "link-local", own: "link-local"},
		"::ffff:0:a9fe:a9fe": {blocked: "link-local", own: "link-local"},
	} {
		parsed := stdnet.ParseIP(ip)
		if got := reasonFor(parsed, false); !strings.Contains(got, want.blocked) {
			t.Errorf("%s refused as %q, want %q", ip, got, want.blocked)
		}
		got := reasonFor(parsed, true)
		if want.own == "" && got != "" || !strings.Contains(got, want.own) {
			t.Errorf("%s with the flag: %q, want %q", ip, got, want.own)
		}
	}
}

func TestAPersonAtTheTerminalReachesAServiceOnTheIPv6Loopback(t *testing.T) {
	useRealBlocklist(t)
	ln, err := stdnet.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("no IPv6 loopback here: %v", err)
	}
	srv := httptest.NewUnstartedServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("mine"))
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "local-network": true}).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatalf("%s: %v", srv.URL, err)
	}
	if !strings.Contains(pairsOf(t, v)["body"], "mine") {
		t.Errorf("the service was not reached: %v", pairsOf(t, v))
	}
	_, err = doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "local-network": true}).WithSurface(plugin.SurfaceMCP))
	if verr := view.AsError(err, "x"); err == nil || verr.Code != "http.request.blocked" {
		t.Errorf("over MCP the flag opened the IPv6 loopback: %v", err)
	}
}

// `localhost` is a name for two addresses on most machines and a service
// listens on one of them: the first the resolver lists is not always it.
func TestLocalhostIsReachedWhicheverAddressTheServiceIsOn(t *testing.T) {
	useRealBlocklist(t)
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("mine"))
	}))
	defer srv.Close()
	_, port, _ := stdnet.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))

	v, err := doRequest(context.Background(), "GET",
		req(map[string]any{"url": "http://localhost:" + port + "/", "local-network": true}).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pairsOf(t, v)["body"], "mine") {
		t.Errorf("the service was not reached: %v", pairsOf(t, v))
	}
}

// The reason a refusal gives is the one that refused: under the flag an address
// is turned away for what the flag does not cover, and "a private address" said
// of the one AWS serves metadata at over IPv6 contradicted the hint that offered
// the flag for private addresses.
func TestARefusalUnderTheFlagNamesWhatTheFlagDoesNotCover(t *testing.T) {
	e := &blockedAddrError{host: "fd00:ec2::254", ip: stdnet.ParseIP("fd00:ec2::254"), own: true}
	if got := e.Error(); !strings.Contains(got, "instance metadata") || strings.Contains(got, "private") {
		t.Errorf("the refusal said %q", got)
	}
}

// A service of one's own is almost never behind TLS, and `rta http get
// localhost:8080 --local-network` went to https:// and failed on the handshake
// with a server that was never going to speak it. The scheme follows where the
// host is, decided from the text alone: a name that merely might resolve
// somewhere private is not guessed at, because the scheme is chosen before the
// address is looked up and a name that answers differently a moment later must
// not turn a request that was meant to be encrypted into a plain one.
func TestASchemeIsDefaultedByWhereTheHostIs(t *testing.T) {
	for _, c := range []struct {
		in, want string
		own      bool
	}{
		{"localhost:8080", "http://localhost:8080", true},
		{"LocalHost:8080/api?x=1", "http://LocalHost:8080/api?x=1", true},
		{"svc.localhost", "http://svc.localhost", true},
		{"127.0.0.1:8080", "http://127.0.0.1:8080", true},
		{"[::1]:8080/x", "http://[::1]:8080/x", true},
		{"10.0.0.5:3000", "http://10.0.0.5:3000", true},
		{"192.168.1.1", "http://192.168.1.1", true},
		{"localhost:443", "https://localhost:443", true},
		{"10.0.0.5:443/x", "https://10.0.0.5:443/x", true},
		{"localhost:8080", "https://localhost:8080", false},
		{"example.com", "https://example.com", true},
		{"8.8.8.8:80", "https://8.8.8.8:80", true},
		{"169.254.169.254", "https://169.254.169.254", true},
		{"grafana.internal:3000", "https://grafana.internal:3000", true},
		{"http://localhost:1", "http://localhost:1", true},
		{"https://10.0.0.5:3000", "https://10.0.0.5:3000", true},
	} {
		if got := withScheme(c.in, c.own); got != c.want {
			t.Errorf("withScheme(%q, own=%v) = %q, want %q", c.in, c.own, got, c.want)
		}
	}
}

func TestTheSchemeDefaultReachesAServiceOfOne(t *testing.T) {
	useRealBlocklist(t)
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		_, _ = w.Write([]byte("mine"))
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET",
		req(map[string]any{"url": strings.TrimPrefix(srv.URL, "http://"), "local-network": true}).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pairsOf(t, v)["body"], "mine") {
		t.Errorf("the service was not reached: %v", pairsOf(t, v))
	}
}
