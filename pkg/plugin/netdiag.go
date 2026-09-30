package plugin

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"runtime"
	"strings"
	"syscall"
	"time"
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
// came back untyped, and the reader was told everything but the CA.
//
// So the answers live here, once, as predicates rather than as one
// classifier, each answering only for what it names. That is a property the
// dial's two have to work for, and they did not always have it: they read an
// error's words when its chain holds no errno, as a driver that flattened
// its dial leaves it, and a server that was reached answers in words it
// chose — a certificate's names, the system's verdict quoting one. A
// certificate named for "connect: connection refused" was nothing listening
// to any plugin that asked the dial's questions before the certificate's,
// on a port that had answered with a certificate. They now answer no error
// that holds a handshake's or a certificate's failure (dialFailed), and so
// none of these takes another's.
//
// **One order is still the plugin's to keep: its driver's own errors
// first.** A server's coded answer, a gRPC status a server gave, a reply a
// driver quotes when it cannot parse it — whatever a server said, read by
// what it is before any of these is asked, since a server's words relayed
// with nothing typed under them can spell a dial's failure as a dial does,
// and no reading of words can tell the two apart. A DNS failure is none of
// these: a dial that could not resolve its host arrives as a *net.OpError
// too, and it is read as the *net.DNSError it wraps — which these leave to
// the plugin, whatever words the resolver's own failed exchange put in it.

// DialRefused reports whether err is a connection the host refused: an
// address that answered, with nothing listening on the port. The port is
// what to check, or whether the server is up.
//
// By the operating system's own error, not by the *net.OpError around it,
// which every failed dial is. A driver that flattens the dial's error into a
// message of its own — gRPC's status carries the text alone — is read by the
// words that error has on this machine as a dial spells them, "connect:
// connection refused" on Linux and macOS, since the dial it flattened ran
// here; but only when nothing in the chain names an error of its own, which
// is the one read then, and never when the chain holds a handshake's or a
// certificate's failure, which only a server that was reached gives
// (dialFailed). A server's own answer is read before this, since a message
// relaying a dial of the server's that failed reads the same.
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
// the chain names no error of the operating system's at all, in its text as
// a dial spells it.
//
// Never for a name that did not resolve. Go's resolver keeps the words of
// its own failed exchange with the DNS server and no error under them — a
// *net.DNSError holds "connect: network is unreachable" about the server's
// address, or "connection refused" from a port nothing answers DNS on — and
// read by them, a name nothing resolved was a host there was no way to, or a
// port refused on an address the name never had.
//
// **Never for an error a server that was reached gave (handshook).** A
// certificate's names are the server's to choose, and so is the name the
// system's verdict quotes, and read for the dial's words a certificate named
// "connect: connection refused" was a port nothing listens on — asked before
// CertUntrusted, as a plugin was free to ask them. Typed or flattened: gRPC's
// status keeps a handshake's words and nothing under them. So too when the
// chain holds a failed dial beside the handshake, as a driver dialling each of
// a name's addresses joins them: the host was reached on one of them, and the
// certificate is what to say.
//
// **And words are read only as a dial spells them, the call that failed
// before them** (dialCalls): "connect: connection refused", never
// "connection refused" alone. A server's own message, relayed untyped by a
// driver, said "connection refused" about something of its own — a backend
// it could not reach — and was read as a port nothing listened on, where the
// server had just answered. No reading of words closes that for good: a
// server can spell a dial's words too, which is why a plugin reads whatever
// its driver says a server said before asking this.
func dialFailed(err error, errnos []syscall.Errno) bool {
	var dnsErr *net.DNSError
	if err == nil || errors.As(err, &dnsErr) || handshook(err) {
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
		for _, call := range dialCalls {
			if strings.Contains(text, call+": "+e.Error()) {
				return true
			}
		}
	}
	return false
}

// dialCalls are the calls a failed TCP dial names before the operating
// system's error, as Go's *os.SyscallError spells it: connect on a Unix, and
// connectex, the overlapped connect, on Windows.
var dialCalls = []string{"connect", "connectex"}

// handshook reports whether err holds a TLS handshake's failure or a verdict
// on a certificate: something only a server that was reached gives. By type,
// for a chain that kept them, and by the words Go's TLS and x509 errors
// open with, "tls: " and "x509: ", for one a driver flattened into text —
// gRPC's status. A received alert is among them: Go spells it "remote
// error: tls: " and the alert's name.
func handshook(err error) bool {
	for _, target := range []any{new(*tls.CertificateVerificationError), new(tls.RecordHeaderError),
		new(tls.AlertError), new(*tls.ECHRejectionError), new(x509.HostnameError),
		new(x509.UnknownAuthorityError), new(x509.CertificateInvalidError), new(x509.SystemRootsError),
		new(x509.ConstraintViolationError), new(x509.UnhandledCriticalExtension),
		new(x509.InsecureAlgorithmError)} {
		if errors.As(err, target) {
			return true
		}
	}
	text := err.Error()
	return strings.Contains(text, "tls: ") || strings.Contains(text, "x509: ")
}

// CertUntrusted reports whether err is a certificate nothing here vouches
// for: its issuer is in no pool the check read, or there was no pool to
// read. The CA that issued it — a self-signed certificate is its own — is
// what the reader gives, in the plugin's CA file setting.
//
// Go's verifier says so as an x509.UnknownAuthorityError, and it is the one
// that runs whenever a CA file is set, on every system, and on Linux always.
// With none set, macOS and iOS ask the system's verifier instead, which Go
// types for three of its verdicts — an expired certificate, a host it is not
// for, a root nothing trusts (errSecNotTrusted, as UnknownAuthorityError) —
// and passes on every other one as a bare error inside the handshake's
// *tls.CertificateVerificationError, carrying the system's words and not its
// code: "x509: ", the certificate's name in curly quotes, and "certificate is
// revoked".
//
// **Only the untyped verdict known to mean an issuer nothing vouches for is
// read as untrusted: the system's "certificate is not trusted".** It is what
// a leaf answers whose chain reaches no anchor the system holds — a
// private CA's, sent without its CA (errSecCreateChainFailed) — and the
// words the system gives errSecNotTrusted itself. Every other untyped
// verdict is its own reason, and a CA file is no cure for it but a way
// around it: naming one runs Go's verifier in the system's place, which
// checks no revocation, no Certificate Transparency and none of Apple's
// policy. Read as untrusted, a certificate the system answered "is revoked"
// was answered with the CA to name, and the operator who named it connected
// to a server with a revoked certificate, the check that caught it gone.
// "Not standards compliant" is not read as untrusted either, though a
// self-signed certificate valid for longer than Apple's policy allows is
// answered with it: a trusted CA's certificate is too, for a critical
// extension the system does not handle or a name its CA may not sign.
//
// By the words' ending, from the closing quote on, which the system's format
// fixes and the certificate's own name, spelled inside the quotes, cannot
// move: a revoked certificate whose name is a closing quote and "certificate
// is not trusted" still ends "is revoked". In the system's English, which is
// what a process with no language of its own is answered in, whatever
// language the person at the Mac reads; a verdict localised into another is
// not read as untrusted, and is left to be answered with the system's words,
// as every verdict CertUntrusted does not read is. Elsewhere an untyped
// answer is one of Go's own errors, never a verdict on trust, and is not
// read as one.
//
// **Windows is the one system whose verdict Go does not tell apart.** With
// no CA file given it asks the system's verifier too, and types every chain
// the system refuses for a reason other than a date or a use as
// UnknownAuthorityError, with nothing else in it: a certificate Windows
// distrusts reads as one from an unknown CA. It is read as untrusted all the
// same, since a private CA is by far the likelier reason and nothing in the
// error tells the two apart — which is why CAHint says what naming a CA
// file goes around there too: Windows' own checks, its list of distrusted
// certificates among them.
func CertUntrusted(err error) bool { return certUntrusted(runtime.GOOS, err) }

// certUntrusted is CertUntrusted on goos, whose verifier answered err.
func certUntrusted(goos string, err error) bool {
	var unknown x509.UnknownAuthorityError
	var noRoots x509.SystemRootsError
	if errors.As(err, &unknown) || errors.As(err, &noRoots) {
		return true
	}
	_, said := systemVerdict(goos, err, "certificate is not trusted")
	return said
}

// CertRevoked reports whether err is a certificate its issuer revoked, as the
// system's verifier said so: macOS's and iOS's, whose "certificate is
// revoked" Go passes on untyped (CertUntrusted says how that verdict is
// spelled, and how it is read by its ending, whatever the certificate's name
// holds).
//
// **For a caller that reads a certificate by what it lacks, to leave
// revocation out explicitly.** A certificate that names no host, or one from
// a CA nothing holds, has a way round it — a mode that checks the chain
// without a name, a CA file — and every way round runs Go's verifier in the
// system's place, which checks no revocation. A plugin that read "no name"
// off a certificate the system had refused for any reason answered a revoked
// one with the mode that connected to it; asked first, this keeps that
// verdict the system's own, to be answered in its words and with no way
// round. CertUntrusted never reads a revoked certificate as untrusted, so the
// CA-file hint needs no such guard.
//
// **Only where a verifier says so.** Go's own verifier, which runs on Linux
// and wherever a CA file is set, checks no revocation at all, and Windows'
// revocation verdict reaches Go as an unknown authority with nothing to tell
// it apart (CertUntrusted): on those, a revoked certificate is not known as
// one, and this answers false.
func CertRevoked(err error) bool { return certRevoked(runtime.GOOS, err) }

// certRevoked is CertRevoked on goos, whose verifier answered err.
func certRevoked(goos string, err error) bool {
	_, said := systemVerdict(goos, err, "certificate is revoked")
	return said
}

// systemVerdict is the handshake's error when err is the verdict of Apple's
// own verifier, on goos, in words ending verdict — one Go passes on untyped
// (CertUntrusted says which it types) — and whether it is.
func systemVerdict(goos string, err error, verdict string) (*tls.CertificateVerificationError, bool) {
	if goos != "darwin" && goos != "ios" {
		return nil, false
	}
	var verifyErr *tls.CertificateVerificationError
	if !errors.As(err, &verifyErr) || verifyErr.Err == nil {
		return nil, false
	}
	// A reason Go types is that reason, whatever its words say.
	for _, reason := range []any{new(x509.HostnameError), new(x509.CertificateInvalidError),
		new(x509.InsecureAlgorithmError), new(x509.UnhandledCriticalExtension),
		new(x509.ConstraintViolationError)} {
		if errors.As(verifyErr.Err, reason) {
			return nil, false
		}
	}
	// Go's darwin verifier spells an untyped verdict as "x509: " and the
	// system's description, which opens on the certificate's name in curly
	// quotes.
	open, closing := string(rune(0x201c)), string(rune(0x201d))
	text := verifyErr.Err.Error()
	return verifyErr, strings.HasPrefix(text, "x509: "+open) && strings.HasSuffix(text, closing+" "+verdict)
}

// CertPolicyHint is the hint for a certificate Apple's verifier refused by a
// rule of Apple's own, which the rule and its fix name, or "" when err is no
// such refusal — to be given beside the refusal a handler words about it,
// as CAHint is beside CertUntrusted.
//
// **One rule is read: the validity period.** macOS and iOS take a TLS server
// certificate issued since 1 July 2019 only when it is valid for at most 825
// days, whoever issued it, and refuse a longer one as "not standards
// compliant" — the words a ten-year self-signed certificate, the usual shape
// of one made for a lab or a cluster, is answered with, and all its reader
// was told. The same words answer other rules — a critical extension the
// system does not handle, a name its CA may not sign — so they are read as
// this one only when the certificate the server sent breaks it, read from the
// handshake's own copy of it, and are answered with the system's words alone
// otherwise, as before.
//
// **The fix is the certificate, and the hint never names a CA file.** A CA
// file would get past the refusal, since naming one runs Go's verifier in
// the system's place, and it would do so by going around every check the
// system makes (CAHint), for a certificate that is not untrusted at all.
func CertPolicyHint(err error) string { return certPolicyHint(runtime.GOOS, err) }

// Apple's limit on a TLS server certificate's validity period, and the date
// from which a certificate is held to it.
var (
	appleValiditySince = time.Date(2019, time.July, 1, 0, 0, 0, 0, time.UTC)
	appleValidityDays  = 825
)

// certPolicyHint is CertPolicyHint on goos, whose verifier answered err.
func certPolicyHint(goos string, err error) string {
	verifyErr, said := systemVerdict(goos, err, "certificate is not standards compliant")
	if !said || len(verifyErr.UnverifiedCertificates) == 0 {
		return ""
	}
	leaf := verifyErr.UnverifiedCertificates[0]
	valid := leaf.NotAfter.Sub(leaf.NotBefore)
	if leaf.NotBefore.Before(appleValiditySince) || valid <= time.Duration(appleValidityDays)*24*time.Hour {
		return ""
	}
	system := "macOS"
	if goos == "ios" {
		system = "iOS"
	}
	return fmt.Sprintf("%s takes a TLS server certificate issued since July 2019 only when it is valid for "+
		"at most %d days, and this one is valid for %d: reissue it valid for %d days or fewer, or 398 when "+
		"a public certificate authority issues it", system, appleValidityDays, int(valid.Hours()/24), appleValidityDays)
}

// CAHint is the hint for a refusal CertUntrusted answered: the CA that
// issued the certificate belongs in setting, the plugin's CA file, named the
// way the reader on s changes it (SettingName) — and what giving one does.
//
// It says the cost, since the cure is the one that runs another check: a CA
// file replaces the system's checks with Go's verifier and that CA alone. On
// macOS and iOS that is the system's revocation, Certificate Transparency and
// policy checks no longer run, which an operator told only "name the CA"
// learned from nothing — and one who named it to get past a verdict that was
// no untrusted issuer's went around the check that gave it. On Windows it is
// the system's checks as well, not only its roots: Go asks the system's
// verifier there too when no CA file is set, and a certificate Windows
// distrusts reaches CertUntrusted as an unknown authority, which only this
// hint's words can warn about.
func (s Surface) CAHint(setting string) string { return s.caHint(runtime.GOOS, setting) }

// caHint is CAHint on goos.
func (s Surface) caHint(goos, setting string) string {
	hint := "the CA that issued it belongs in " + s.SettingName(setting) +
		" (a self-signed certificate is its own CA), and a CA file replaces the system's "
	switch goos {
	case "darwin", "ios":
		return hint + "checks: the certificate is then checked against that CA alone, with none of the revocation " +
			"and policy checks macOS makes"
	case "windows":
		return hint + "checks: the certificate is then checked against that CA alone, with none of the checks " +
			"Windows makes, its list of distrusted certificates among them"
	}
	return hint + "roots: the certificate is then checked against that CA alone"
}
