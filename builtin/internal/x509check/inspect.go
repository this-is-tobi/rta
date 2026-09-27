package x509check

import "crypto/tls"

// InspectionTLS is the client side of a handshake made to look at a host,
// never to trust it: it offers every protocol and suite Go can speak, and
// verifies nothing. serverName is the name sent in the handshake, or "" for
// a caller whose transport sets it.
//
// A capability that inspects what a host negotiates cannot hold the host to
// the floor a client trusting the answer would. Go's client refuses below
// TLS 1.2 and no longer proposes 3DES or RSA key exchange, and a host
// speaking only those failed the handshake: audit web, cert and net probe
// each answered that the host could not be reached, about a host that
// answered, and the protocol it spoke — the finding — was never seen.
// Offering them costs nothing against a host that has better, since the host
// chooses; against one that does not, what it chose is the answer.
//
// For a handshake that inspects and nothing more. One that carries a
// caller's bytes to the host, net send's, keeps Go's floor, since a payload
// has no business crossing a protocol retired for being breakable.
func InspectionTLS(serverName string) *tls.Config {
	all := append(tls.CipherSuites(), tls.InsecureCipherSuites()...)
	suites := make([]uint16, len(all))
	for i, s := range all {
		suites[i] = s.ID
	}
	return &tls.Config{
		ServerName:         serverName,
		InsecureSkipVerify: true,             //nolint:gosec // inspected, not trusted: what the host presents is the answer
		MinVersion:         tls.VersionTLS10, //nolint:gosec // a deprecated protocol is reported, not refused
		CipherSuites:       suites,
	}
}

// DeprecatedTLS reports whether version is a protocol RFC 8996 retired, TLS
// 1.0 or 1.1, which a capability that grades anything grades as a weakness.
func DeprecatedTLS(version uint16) bool { return version < tls.VersionTLS12 }
