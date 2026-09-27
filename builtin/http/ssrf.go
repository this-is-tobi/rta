package http

import (
	"context"
	"fmt"
	stdnet "net"
	stdhttp "net/http"
	"net/netip"
	"net/url"
	"strings"
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
// hostname now means, not what an operator saw and approved. Clouds publish
// instance credentials on an address meant to be unreachable from outside
// the machine — link-local 169.254.169.254 for most, shared address space
// 100.100.100.200 for Alibaba's — and an http plugin that dials wherever DNS
// points defeats that in one request.
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
// file's capabilities offers one. The guard holds at the terminal as well:
// it lives in the one client every http.* call shares, whoever makes it, so
// an operator who genuinely needs their own internal service reaches it
// with a client of their own. Accepting a caller-supplied "yes, this one
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

// defaultBlockedIP refuses every address that is not on the public
// internet — see blockedReason.
func defaultBlockedIP(ip stdnet.IP) bool { return blockedReason(ip) != "" }

// blockedReason says why rta refuses to dial ip, as the noun phrase a
// refusal names it by, or "" when it does not.
//
// Go's own predicates first: loopback, RFC 1918/4193 private, link-local
// (169.254.0.0/16 is where most clouds' instance metadata lives), multicast
// and the unspecified address. Unmap first, so an IPv4-mapped IPv6 address,
// ::ffff:127.0.0.1 and its kin, is tested as the IPv4 address it is.
//
// **Those predicates are not "not public", which is what this needs.**
// IsPrivate is RFC 1918 and nothing else, so 100.64.0.0/10 passed — the
// shared address space Alibaba Cloud serves its metadata from, at
// 100.100.100.200, and every Tailscale node's address — while the refusal
// promised the agent that cloud metadata was out of reach. reserved lists
// the ranges no public service lives in that the predicates miss.
//
// **And an IPv4 address can arrive inside an IPv6 one.** On a NAT64 network
// 64:ff9b::a9fe:a9fe is 169.254.169.254 to the translator that forwards it,
// and 6to4, the IPv4-compatible form, SIIT's IPv4-translated form and Teredo
// carry one the same way, so the address inside is judged as if it had been
// dialed directly. The local-use NAT64 prefix is refused whole instead: where
// an operator puts the IPv4 address inside it is theirs to choose, so nothing
// here can read it back out.
func blockedReason(ip stdnet.IP) string {
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return "an address of neither family"
	}
	addr = addr.Unmap()
	if why := addrReason(addr); why != "" {
		return why
	}
	if inner, form, ok := embeddedIPv4(addr); ok {
		if why := addrReason(inner); why != "" {
			return fmt.Sprintf("the %s form of %s, %s", form, inner, why)
		}
	}
	return ""
}

// reserved is the ranges blockedReason refuses that Go's predicates do not
// cover. 240.0.0.0/4 takes the broadcast address with it.
var reserved = []struct {
	prefix netip.Prefix
	why    string
}{
	{netip.MustParsePrefix("0.0.0.0/8"), `an address in "this network", 0.0.0.0/8`},
	{netip.MustParsePrefix("100.64.0.0/10"), "shared address space (RFC 6598), where carrier NAT, " +
		"Tailscale and Alibaba Cloud's instance metadata live"},
	{netip.MustParsePrefix("192.0.0.0/24"), "an IETF protocol address, 192.0.0.0/24"},
	{netip.MustParsePrefix("198.18.0.0/15"), "a benchmarking address, 198.18.0.0/15"},
	{netip.MustParsePrefix("240.0.0.0/4"), "a reserved address, 240.0.0.0/4"},
	{netip.MustParsePrefix("fec0::/10"), "a site-local address"},
	{netip.MustParsePrefix("64:ff9b:1::/48"), "a local-use NAT64 address (RFC 8215)"},
}

func addrReason(a netip.Addr) string {
	switch {
	case a.IsUnspecified():
		return "the unspecified address"
	case a.IsLoopback():
		return "a loopback address"
	case a.IsPrivate():
		return "a private address"
	case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
		return "a link-local address"
	case a.IsMulticast():
		return "a multicast address"
	}
	for _, r := range reserved {
		if r.prefix.Contains(a) {
			return r.why
		}
	}
	return ""
}

var (
	nat64          = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour      = netip.MustParsePrefix("2002::/16")
	ipv4Compatible = netip.MustParsePrefix("::/96")
	ipv4Translated = netip.MustParsePrefix("::ffff:0:0:0/96")
	teredo         = netip.MustParsePrefix("2001::/32")
)

// embeddedIPv4 is the IPv4 address an IPv6 address carries for a
// translator or a tunnel to deliver to, and the name of the form.
//
// The IPv4-translated form (RFC 2765, SIIT) is not the IPv4-mapped one Unmap
// already reads: ::ffff:0:a9fe:a9fe has a zero word after the ffff one, so
// it is neither mapped nor in ::/96, and a stateless translator hands it to
// 169.254.169.254. A Teredo address (RFC 4380) holds two IPv4 addresses, and
// the one judged is the client's in its last 32 bits, stored with every bit
// inverted so that no NAT on the way rewrites it: that is where a relay, or
// the machine's own Teredo interface, sends the packet. The server's, after
// the prefix, is where the client registered and is never dialed.
func embeddedIPv4(a netip.Addr) (netip.Addr, string, bool) {
	b := a.As16()
	switch {
	case !a.Is6():
		return netip.Addr{}, "", false
	case nat64.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), "NAT64", true
	case sixToFour.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6])), "6to4", true
	case ipv4Compatible.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), "IPv4-compatible", true
	case ipv4Translated.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), "IPv4-translated", true
	case teredo.Contains(a):
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), "Teredo", true
	}
	return netip.Addr{}, "", false
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
	why := blockedReason(e.ip)
	if why == "" {
		// Only a test's relaxed or tightened isBlockedIP gets here.
		why = "an address"
	}
	return fmt.Sprintf("%s resolves to %s, %s — rta refuses to connect there", e.host, e.ip, why)
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
	return req.WithContext(context.WithValue(req.Context(), trustedProxyKey{}, proxyAddr(proxyURL)))
}

// defaultProxyPort is the port the Transport dials a proxy named without
// one at, by the proxy's scheme.
var defaultProxyPort = map[string]string{"http": "80", "https": "443", "socks5": "1080", "socks5h": "1080"}

// proxyAddr is the address the Transport dials to reach proxy u — net/http's
// own canonicalAddr, which is unexported, so its rule is restated here: the
// host, and the port, the scheme's default when u names none.
//
// The mark used to be u.Host as written, and dialGuarded compares it with
// the address the Transport dials. "http://proxy.corp" is dialed as
// "proxy.corp:80", so the two never met, and the operator's own proxy on a
// private or loopback address — the ordinary corporate egress, a local
// mitmproxy — was run through the blocklist and refused, with a refusal
// blaming the destination.
//
// Short of canonicalAddr in one way, on purpose: net/http dials an
// internationalised proxy name in its xn-- form, and converting it here
// would take golang.org/x/net/idna and x/text's mapping tables into the
// binary, some fifty kilobytes, for a proxy nobody names that way. Such a
// name never matches the mark, so its dial is judged as any destination's
// is — refused if it resolves somewhere private, never let through
// unchecked — and the proxy named in its xn-- form, the one its zone is
// published under, matches.
func proxyAddr(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = defaultProxyPort[u.Scheme]
	}
	return stdnet.JoinHostPort(u.Hostname(), port)
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
	// Case-blind, as a host name is: net/http keeps an ASCII host's case as
	// the URL wrote it, and "PROXY.corp:80" is the same proxy.
	if trusted, ok := ctx.Value(trustedProxyKey{}).(string); ok && strings.EqualFold(trusted, addr) {
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
