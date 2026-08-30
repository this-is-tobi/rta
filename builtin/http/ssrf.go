package http

import (
	"context"
	"fmt"
	stdnet "net"
	stdhttp "net/http"
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

// dialGuarded is client's Transport.DialContext. It resolves host itself
// instead of leaving that to the dialer, checks every address host
// resolved to against isBlockedIP, and — only once none of them are
// blocked — dials the specific IP it just checked. See the block comment
// above for why the check has to live here, at dial time, rather than
// anywhere upstream of it.
func dialGuarded(ctx context.Context, network, addr string) (stdnet.Conn, error) {
	host, port, err := stdnet.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
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
	// The literal address just validated, not host:port again: asking the
	// dialer to resolve host a second time is the TOCTOU this guard exists
	// to close, not a detail it can afford to reintroduce.
	dialer := &stdnet.Dialer{}
	return dialer.DialContext(ctx, network, stdnet.JoinHostPort(ips[0].IP.String(), port))
}

// guardedTransport clones stdhttp.DefaultTransport — keeping its proxy
// support (rta's http plugin reaching through HTTP(S)_PROXY via
// DefaultTransport is documented behavior; see builtin/net's maskProxy) and
// its connection pooling — and swaps in dialGuarded as the one thing that
// has to change.
func guardedTransport() *stdhttp.Transport {
	t := stdhttp.DefaultTransport.(*stdhttp.Transport).Clone()
	t.DialContext = dialGuarded
	return t
}
