package http

import (
	"context"
	"fmt"
	stdnet "net"
	stdhttp "net/http"
	"net/url"
)

// Where an http.* call is allowed to connect, decided at the moment it
// connects.
//
// A grant on this package's capabilities (Scope: "url") is checked once,
// against the URL string named, before Run ever executes — see client's own
// comment for the redirect half of this problem. DNS is the other half: if
// the name in that URL resolves to a different address by the time the
// request actually dials — a low-TTL record, a zone the caller doesn't
// control, a misconfigured CDN, or simply a caller asking for
// "169.254.169.254" directly — the grant has authorized whatever that
// hostname now means, not what an operator saw and approved. Every major
// cloud publishes instance credentials on a link-local address for exactly
// this to be hard to reach from outside the machine; an http plugin that
// dials wherever DNS points defeats that in one request.
//
// So the check below runs inside the dialer, against the IP about to be
// dialed, not against the URL's hostname string — parsing the string proves
// nothing about where the connection actually lands. And once it has
// resolved and checked a name, it dials the exact IP it just validated
// rather than handing the hostname back to the network stack for a second
// lookup: a second resolution can legitimately return a different answer
// than the first — that is the whole attack, for a low-TTL or
// attacker-controlled name — so re-resolving between the check and the
// connect would silently reopen the gap this exists to close.
//
// There is no opt-in to reach these addresses anyway, and no flag on this
// file's capabilities offers one. An operator who genuinely needs to reach
// their own internal service already can, from outside a grant — the CLI
// and the TUI are never gated. Accepting a caller-supplied "yes, this one
// is fine" would hand exactly that decision to whoever holds the grant,
// which is the consent a grant exists to require in the first place.
//
// **A configured proxy (HTTP(S)_PROXY, which client's own comment already
// says this file supports) used to turn all of the above off.** With one
// set, Transport dials the *proxy's* address, not the target's — the
// destination rides inside the CONNECT line instead — so dialGuarded was
// validating the wrong address and letting the real one through
// unexamined, while a proxy that happened to sit on loopback (an mitmproxy,
// a corporate egress) failed every request for naming a "blocked" address
// the caller never chose. checkDestination and withTrustedProxy below are
// the fix, run once per request in doRequest before client.Do: the real
// destination is checked before anything picks a route at all, and the
// proxy the environment names — never the caller — is marked trusted so
// dialGuarded stops treating it as a caller-chosen address.

// isBlockedIP decides whether dialGuarded refuses ip. A package var holding
// defaultBlockedIP, rather than that function called directly, so tests can
// relax it enough to reach the loopback servers httptest binds to without
// disabling the check this file is actually about — see http_test.go's
// TestMain and the tests in ssrf_test.go, which restore it deliberately.
var isBlockedIP = defaultBlockedIP

// defaultBlockedIP refuses loopback, RFC 1918/4193 private ranges,
// link-local (169.254.0.0/16 and fe80::/10 — the first is where cloud
// instance metadata lives) and the unspecified address. IsPrivate and the
// rest already unwrap an IPv4-mapped IPv6 address (::ffff:127.0.0.1 and
// its kin) before testing it, so there is no separate case for that form.
func defaultBlockedIP(ip stdnet.IP) bool {
	return ip.IsUnspecified() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast()
}

// blockedAddrError is dialGuarded's refusal, kept as its own type rather
// than a plain fmt.Errorf so doRequest can tell "the destination is on the
// blocklist" apart from "the network failed" and give each its own
// view.Error code — telling someone to raise --timeout or check the URL is
// reachable is the wrong advice for a request refused on purpose.
type blockedAddrError struct {
	host string
	ip   stdnet.IP
}

func (e *blockedAddrError) Error() string {
	return fmt.Sprintf("%s resolves to %s, a loopback, private, or link-local address rta refuses to connect to",
		e.host, e.ip)
}

// resolveAndCheck resolves host and refuses if any address it answers with
// is on the blocklist, returning the resolved addresses so a caller that
// goes on to dial can reuse the exact one just validated instead of asking
// the network stack to resolve host a second time — a second resolution can
// legitimately return a different answer than the first, which is the
// TOCTOU this whole file exists to close.
func resolveAndCheck(ctx context.Context, host string) ([]stdnet.IPAddr, error) {
	ips, err := stdnet.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s: no addresses found", host)
	}
	for _, resolved := range ips {
		if isBlockedIP(resolved.IP) {
			return nil, &blockedAddrError{host: host, ip: resolved.IP}
		}
	}
	return ips, nil
}

// trustedProxyKey marks a request context as having a proxy the operator's
// own environment named, not the caller — see withTrustedProxy.
type trustedProxyKey struct{}

// proxyFunc decides which proxy, if any, a request should go through. A
// var wrapping stdhttp.ProxyFromEnvironment rather than a direct call to
// it, so a test can replace the decision: net/http caches its own
// environment read behind a process-wide sync.Once, which makes setting
// HTTP_PROXY/HTTPS_PROXY at test time unreliable the moment any earlier
// test in the same process has already sent a request.
var proxyFunc = stdhttp.ProxyFromEnvironment

// withTrustedProxy decides, once, whether req would be proxied under the
// current environment, and if so marks that proxy's own address as trusted
// before the request is sent. dialGuarded reads the mark to tell "dialing
// the proxy the environment named" apart from "dialing wherever the
// caller's URL resolves", which unlike the proxy is not the operator's
// choice and is exactly what the blocklist exists to check.
//
// Calling proxyFunc here rather than only leaving it to the Transport
// changes no routing decision — guardedTransport routes through the same
// var — it only lets the answer be known before the dial happens instead
// of only inside it, which is what lets dialGuarded tell the two dials
// apart at all.
func withTrustedProxy(req *stdhttp.Request) *stdhttp.Request {
	proxyURL, err := proxyFunc(req)
	if err != nil || proxyURL == nil {
		return req
	}
	return req.WithContext(context.WithValue(req.Context(), trustedProxyKey{}, proxyURL.Host))
}

// checkDestination refuses a request whose URL resolves to a blocked
// address, before anything is sent and before a proxy is even chosen.
//
// Without this, a configured proxy turned the blocklist off entirely: the
// address dialGuarded went on to validate was the proxy's, and the real
// destination — u.Host — rode inside the CONNECT line untouched by any
// check in this file. A proxied request never reaches dialGuarded's own
// resolve-and-pin logic for the target at all, because the address it
// dials is the proxy's, not the target's, so this is the only check the
// target ever gets on that path.
//
// What it cannot close: the proxy resolves and connects to the target on
// its own, independently of this lookup, so a low-TTL record or a DNS view
// that differs between rta and the proxy can still mean the proxy reaches
// somewhere this check did not see. That residual gap is inherent to
// routing through a forward proxy at all — rta's own guard does not hold
// the connection past the CONNECT — and is not something a second lookup
// here would close.
func checkDestination(ctx context.Context, u *url.URL) error {
	host := u.Hostname()
	if host == "" {
		return nil
	}
	_, err := resolveAndCheck(ctx, host)
	return err
}

// dialGuarded is client's Transport.DialContext.
//
// A dial to the address withTrustedProxy marked is the environment's own
// proxy, already accounted for by checkDestination before the request was
// sent — the blocklist does not apply a second time to an address the
// caller never chose. Every other dial is resolved here, checked against
// isBlockedIP, and — only once none of the addresses it found are blocked —
// made to the specific IP just validated. See the block comment above for
// why that check has to live here, at dial time, rather than anywhere
// upstream of it, for a direct (unproxied) connection.
func dialGuarded(ctx context.Context, network, addr string) (stdnet.Conn, error) {
	dialer := &stdnet.Dialer{}
	if trusted, ok := ctx.Value(trustedProxyKey{}).(string); ok && trusted == addr {
		return dialer.DialContext(ctx, network, addr)
	}
	host, port, err := stdnet.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := resolveAndCheck(ctx, host)
	if err != nil {
		return nil, err
	}
	// The literal address just validated, not host:port again: asking the
	// dialer to resolve host a second time is the TOCTOU this guard exists
	// to close, not a detail it can afford to reintroduce.
	return dialer.DialContext(ctx, network, stdnet.JoinHostPort(ips[0].IP.String(), port))
}

// guardedTransport clones stdhttp.DefaultTransport — keeping its proxy
// support (rta's http plugin reaching through HTTP(S)_PROXY via
// DefaultTransport is documented behavior; see builtin/net's maskProxy) and
// its connection pooling — and swaps in dialGuarded as the one thing that
// has to change.
//
// Proxy is routed through the proxyFunc var, indirectly, rather than left
// as the stdhttp.ProxyFromEnvironment value Clone() would otherwise carry
// over: the closure reads the var on every call, so a test that replaces
// proxyFunc changes what this Transport actually does, not just what
// withTrustedProxy computes ahead of it. In production the two are the
// same function either way.
func guardedTransport() *stdhttp.Transport {
	t := stdhttp.DefaultTransport.(*stdhttp.Transport).Clone()
	t.DialContext = dialGuarded
	t.Proxy = func(req *stdhttp.Request) (*url.URL, error) { return proxyFunc(req) }
	return t
}
