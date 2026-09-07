package http

import (
	"context"
	"errors"
	stdnet "net"
	stdhttp "net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// withProxy points proxyFunc at a fixed URL for one test — the seam that
// lets a proxy decision be tested without relying on HTTP_PROXY/HTTPS_PROXY
// env vars, which net/http caches process-wide behind a sync.Once the
// moment any request has gone through stdhttp.ProxyFromEnvironment once.
func withProxy(t *testing.T, proxyURL *url.URL) {
	t.Helper()
	saved := proxyFunc
	proxyFunc = func(*stdhttp.Request) (*url.URL, error) { return proxyURL, nil }
	t.Cleanup(func() { proxyFunc = saved })
}

// useRealBlocklist restores the real address policy for one test, undoing
// TestMain's blanket relaxation. Every test in this file needs it: it is
// the guard's own behavior under test, not the plugin's request handling.
func useRealBlocklist(t *testing.T) {
	t.Helper()
	saved := isBlockedIP
	isBlockedIP = defaultBlockedIP
	t.Cleanup(func() { isBlockedIP = saved })
}

func TestDefaultBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",        // loopback
		"127.53.0.1",       // loopback, not just 127.0.0.1 itself
		"::1",              // loopback, IPv6
		"0.0.0.0",          // unspecified
		"::",               // unspecified, IPv6
		"10.0.0.1",         // RFC 1918
		"172.16.0.1",       // RFC 1918
		"172.31.255.255",   // RFC 1918, top of the 172.16/12 block
		"192.168.1.1",      // RFC 1918
		"169.254.169.254",  // link-local: the cloud metadata address
		"169.254.0.1",      // link-local, general
		"fe80::1",          // link-local, IPv6
		"fc00::1",          // unique local, IPv6 (RFC 4193)
		"::ffff:127.0.0.1", // loopback, IPv4-mapped IPv6
		"::ffff:10.1.2.3",  // RFC 1918, IPv4-mapped IPv6
	}
	for _, s := range blocked {
		ip := stdnet.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q does not parse as an IP", s)
		}
		if !defaultBlockedIP(ip) {
			t.Errorf("defaultBlockedIP(%s) = false, want true", s)
		}
	}

	allowed := []string{
		"8.8.8.8",              // public
		"1.1.1.1",              // public
		"93.184.216.34",        // public (example.com, historically)
		"2001:4860:4860::8888", // public, IPv6
		"169.253.255.255",      // just outside 169.254.0.0/16
		"172.32.0.1",           // just outside 172.16.0.0/12
	}
	for _, s := range allowed {
		ip := stdnet.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: %q does not parse as an IP", s)
		}
		if defaultBlockedIP(ip) {
			t.Errorf("defaultBlockedIP(%s) = true, want false", s)
		}
	}
}

// A grant authorizes the URL a caller named, checked once before Run
// starts. Without dialGuarded, that authorization silently covers whatever
// the name resolves to at connection time — loopback included, since
// nothing about the URL string "example.com" says it cannot answer
// 127.0.0.1 today. This is the direct case: asking for a loopback address
// outright.
func TestBlockedAddressesAreRefused(t *testing.T) {
	useRealBlocklist(t)

	cases := []struct {
		name string
		url  string
	}{
		{"loopback", "http://127.0.0.1:9/"},
		{"cloud metadata", "http://169.254.169.254/latest/meta-data/iam/security-credentials/role"},
		{"private RFC 1918", "http://10.1.2.3/"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": c.url}))
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			ve := view.AsError(err, "x")
			if ve.Code != "http.request.blocked" {
				t.Errorf("code = %q, want http.request.blocked (message: %s)", ve.Code, ve.Message)
			}
			if ve.Hint == "" {
				t.Error("blocked request has no hint")
			}
		})
	}
}

// The same refusal, but proving it actually stops the connection rather
// than just rejecting a suspicious-looking string: a real, reachable server
// is standing behind the loopback address and the guard has to win the race
// against it every time, not just when nothing is listening.
func TestBlockedAddressIsNeverActuallyReached(t *testing.T) {
	useRealBlocklist(t)

	var hit bool
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(stdhttp.ResponseWriter, *stdhttp.Request) {
		hit = true
	}))
	defer srv.Close()

	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if hit {
		t.Fatal("the server was actually reached — the guard let a loopback connection through")
	}
	ve := view.AsError(err, "x")
	if ve.Code != "http.request.blocked" {
		t.Errorf("code = %q, want http.request.blocked", ve.Code)
	}
}

// The guard changes how a connection is made (resolve first, then dial the
// literal address it checked) even when nothing is blocked. This confirms
// that plumbing still lands a request against an ordinary server — the
// same shape of check every other test in this package relies on via
// TestMain, pinned here by name against the concern that motivated it.
func TestOrdinaryRequestsStillReachTheServer(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "200") {
		t.Errorf("status = %q", pairs["status"])
	}
}

// A configured proxy used to turn the whole blocklist off: Transport dials
// the proxy's address, not the target's, so dialGuarded validated the
// wrong end of the connection and the real destination — inside the
// CONNECT line — went unchecked. checkDestination runs before doRequest
// ever asks whether a proxy applies, so this must still be refused with a
// proxy configured to reach anywhere at all.
func TestAConfiguredProxyDoesNotBypassTheDestinationCheck(t *testing.T) {
	useRealBlocklist(t)

	var proxyHit bool
	proxy := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		proxyHit = true
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	withProxy(t, proxyURL)

	_, err = doRequest(context.Background(), "GET",
		req(map[string]any{"url": "http://169.254.169.254/latest/meta-data/iam/security-credentials/role"}))
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	ve := view.AsError(err, "x")
	if ve.Code != "http.request.blocked" {
		t.Errorf("code = %q, want http.request.blocked (message: %s)", ve.Code, ve.Message)
	}
	if proxyHit {
		t.Fatal("the proxy was asked to reach the blocked destination — a proxy bypassed the SSRF check")
	}
}

// The other half, and the one that fails a different way: a proxy that
// happens to sit on loopback (an mitmproxy, a corporate egress) is not the
// caller's choice and must not be refused as if it were, merely because
// dialGuarded is about to connect to a loopback address.
func TestALoopbackProxyIsNotRefusedForBeingLoopback(t *testing.T) {
	useRealBlocklist(t)

	var gotRequestURI string
	proxy := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotRequestURI = r.RequestURI
		w.Write([]byte("ok"))
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	withProxy(t, proxyURL)

	// A literal public-looking IP, so checkDestination's lookup needs no
	// real DNS query (LookupIPAddr short-circuits a parseable literal) and
	// the test has no network dependency — the point here is not whether
	// this address is reachable, it never is, only that the proxy (not
	// this address) is what actually gets dialed.
	const target = "http://93.184.216.34/widget"
	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": target}))
	if err != nil {
		t.Fatalf("a request through a loopback proxy was refused: %v", err)
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "200") {
		t.Errorf("status = %q", pairs["status"])
	}
	if !strings.Contains(gotRequestURI, "93.184.216.34") {
		t.Errorf("the proxy never saw the original target: RequestURI = %q", gotRequestURI)
	}
}

// dialGuarded's own two halves, isolated from the request layer above: a
// dial to the address withTrustedProxy marked is let through whatever it
// is, and every other dial still goes through the real blocklist.
func TestDialGuardedTrustsOnlyTheMarkedAddress(t *testing.T) {
	useRealBlocklist(t)

	ctx := context.WithValue(context.Background(), trustedProxyKey{}, "127.0.0.1:1")
	_, err := dialGuarded(ctx, "tcp", "127.0.0.1:1")
	var blocked *blockedAddrError
	if errors.As(err, &blocked) {
		t.Fatalf("a dial to the marked proxy address was refused as blocked: %v", err)
	}
	if err == nil {
		t.Fatal("want a connection error (nothing listens on port 1), got nil")
	}

	_, err = dialGuarded(context.Background(), "tcp", "127.0.0.1:1")
	if !errors.As(err, &blocked) {
		t.Fatalf("an unmarked loopback dial was not refused as blocked: %v", err)
	}
}
