package plugin

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"runtime"
	"strings"
	"syscall"
)

// Reading why a connection failed, for the refusal a handler words about it.
//
// Every connection plugin asked the same three questions of a failed dial —
// was the port refused, was there no way to the host at all, was the
// certificate one nothing here vouches for — and each answered them with
// helpers of its own that went wrong the same way. A *net.OpError was read
// as "nothing is listening", whatever it carried: a server behind a VPN that
// was down, or at an address of a network this machine is not on, was told
// to check a port no packet had reached. And a certificate was read as
// untrusted only as an x509.UnknownAuthorityError, which is how Go's own
// verifier says it — but on macOS, with no CA file given, the system's
// verifier answers, and a private CA's chain ("certificate is not trusted")
// or a self-signed certificate valid for longer than Apple's policy allows
// ("not standards compliant") came back untyped, and the reader was told
// everything but the CA.
//
// So the answers live here, once, as predicates rather than as one
// classifier: each plugin reads its driver's own errors first — a server's
// coded answer, a gRPC status, a handshake its protocol names — and asks
// these in the order its failures need, and each question answers only for
// what it names, so no order among them is needed to keep one from taking
// another's failure. A DNS failure is none of the three: a dial that could
// not resolve its host arrives as a *net.OpError too, and it is read as the
// *net.DNSError it wraps — which these leave to the plugin, whatever words
// the resolver's own failed exchange put in it.

// DialRefused reports whether err is a connection the host refused: an
// address that answered, with nothing listening on the port. The port is
// what to check, or whether the server is up.
//
// By the operating system's own error, not by the *net.OpError around it,
// which every failed dial is. A driver that flattens the dial's error into a
// message of its own — gRPC's status carries the text alone — is read by the
// words that error has on this machine, "connection refused" on Linux and
// macOS, since the dial it flattened ran here; but only when nothing in the
// chain names an error of its own, which is the one read then. A server's
// own answer is read before this, since a message relaying a dial of the
// server's that failed reads the same.
func DialRefused(err error) bool { return dialFailed(err, refusedErrnos) }

// DialUnroutable reports whether err is a dial that found no way to the
// host: no route to it, a network this machine has no way onto or whose
// interface is down, a host its own network reports down. Nothing there
// could refuse the connection, and "nothing is listening" about a port no
// packet reached sent its reader to a server that may be fine — a VPN or a
// tunnel that is down, or an address of another network's, is what to
// check. Read the way DialRefused is, flattened text included.
//
// Never beside a refusal: a driver that dials each of a name's addresses
// joins what each dial answered into one error (pgx does), and a host whose
// IPv6 address this machine has no route to and whose IPv4 one nothing
// listens on answered both. The host was reached, and the port is what to
// check — read as unroutable by a plugin that asks this first, a server that
// was down was a VPN to look at.
func DialUnroutable(err error) bool {
	return dialFailed(err, unroutableErrnos) && !dialFailed(err, refusedErrnos)
}

// dialFailed reports whether err is one of errnos: in its chain, or, when
// the chain names no error of the operating system's at all, in its text.
//
// Never for a name that did not resolve. Go's resolver keeps the words of
// its own failed exchange with the DNS server and no error under them — a
// *net.DNSError holds "connect: network is unreachable" about the server's
// address, or "connection refused" from a port nothing answers DNS on — and
// read by them, a name nothing resolved was a host there was no way to, or a
// port refused on an address the name never had.
func dialFailed(err error, errnos []syscall.Errno) bool {
	var dnsErr *net.DNSError
	if err == nil || errors.As(err, &dnsErr) {
		return false
	}
	for _, e := range errnos {
		if errors.Is(err, e) {
			return true
		}
	}
	var own syscall.Errno
	if errors.As(err, &own) {
		return false
	}
	text := err.Error()
	for _, e := range errnos {
		if strings.Contains(text, e.Error()) {
			return true
		}
	}
	return false
}

// CertUntrusted reports whether err is a certificate nothing here vouches
// for: its issuer is in no pool the check read, or there was no pool to
// read. The CA that issued it — a self-signed certificate is its own — is
// what the reader gives, in the plugin's CA file setting.
//
// Go's verifier says so as an x509.UnknownAuthorityError, and it is the one
// that runs whenever a CA file is set, on every system, and on Linux always.
// With none set, macOS and iOS ask the system's verifier instead, which
// types only an expired certificate, a host the certificate is not for, and
// one of its trust verdicts; every other answer — a private CA's chain, a
// certificate valid for longer than Apple's policy allows — comes back as a
// bare error inside the handshake's *tls.CertificateVerificationError, in
// words the system localises. So there, a verification failure answered
// untyped is read as untrusted, and the CA cures it: given one, Go's own
// verifier runs in the system's place.
//
// Untyped, and not merely not UnknownAuthorityError: Go types every other
// reason it rejects a certificate for — a host it is not for, a date or a
// use it is not valid for, a signature algorithm it will not accept, a
// critical extension it does not handle, a name its constraints exclude —
// and each of those is a reason of its own, which no CA would cure. Read as
// untrusted, a SHA-1 certificate was answered with the CA to name and the
// reason itself unsaid. Elsewhere an untyped answer is one of Go's own
// errors, never a verdict on trust, and is not read as one.
func CertUntrusted(err error) bool { return certUntrusted(runtime.GOOS, err) }

// certUntrusted is CertUntrusted on goos, whose verifier answered err.
func certUntrusted(goos string, err error) bool {
	var unknown x509.UnknownAuthorityError
	var noRoots x509.SystemRootsError
	if errors.As(err, &unknown) || errors.As(err, &noRoots) {
		return true
	}
	if goos != "darwin" && goos != "ios" {
		return false
	}
	var verifyErr *tls.CertificateVerificationError
	if !errors.As(err, &verifyErr) {
		return false
	}
	for _, reason := range []any{new(x509.HostnameError), new(x509.CertificateInvalidError),
		new(x509.InsecureAlgorithmError), new(x509.UnhandledCriticalExtension),
		new(x509.ConstraintViolationError)} {
		if errors.As(verifyErr.Err, reason) {
			return false
		}
	}
	return true
}
