package plugin

import (
	"crypto/x509"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// CertNames lists the names cert is for, as a hostname check reads them —
// its DNS names and its IP addresses, never the subject's common name, which
// Go's verifier no longer reads — the first three and how many more. For a
// refusal of a certificate for its name (x509.HostnameError, whose
// Certificate this takes), which says what the certificate is for beside
// what it was checked for: the way on is one of those names, or a
// certificate that holds the other. "no name a check reads" for one with
// none, and "another name" for no certificate at all.
func CertNames(cert *x509.Certificate) string {
	if cert == nil {
		return "another name"
	}
	names := slices.Clone(cert.DNSNames)
	for _, ip := range cert.IPAddresses {
		names = append(names, ip.String())
	}
	switch {
	case len(names) == 0:
		return "no name a check reads"
	case len(names) > 3:
		return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
	}
	return strings.Join(names, ", ")
}

// ForwardNameRefusal is the refusal for a certificate that was checked for
// the end of a forward the host opened — 127.0.0.1 — and is for the name the
// server answers as instead: the host's own address is not a name any
// certificate carries, so a call through a profile's forward to a server that
// verifies its name fails there, and the way on is the name the certificate
// is for.
//
// **Seven plugins worded this the same, each by hand.** Only the code, the
// input that names the server and what answers it differed (a server, a
// member, an instance), and a hint kept alike by hand in seven places is one
// that drifts: the way through is tls-server-name, which the profile holds
// beside its forward and which is checked as strictly as the host it
// replaces — never a mode that skips the name, which would accept any
// certificate its CA ever signed, and never the plaintext setting the forward
// has already switched off, which the host refuses beside it. A plugin passes
// its own code (pg.tls.forward), the address its call reached as the input
// holds it (Request.Reached words the rest), and what answers.
func ForwardNameRefusal(req Request, code, address, answerer string, hostErr x509.HostnameError) *view.Error {
	return view.Errorf(code, "the certificate behind %s is for %s, not for %s, where the forward ends",
		req.Reached(address), CertNames(hostErr.Certificate), hostErr.Host).
		WithHint("a forward always ends at 127.0.0.1, so the certificate is checked for the name the " + answerer +
			" answers as instead: " + req.Surface().SettingName("tls-server-name") + ", which the profile can " +
			"hold beside its forward, names it — one the certificate is for — and it is checked as strictly " +
			"as the host it replaces")
}

// TLSExpected reports whether err says a plain-HTTP request reached a
// server that speaks only TLS on that port: the request went out in the
// clear, and what came back was the server refusing it, which no change to
// the server cures — the call's own scheme does (an https:// address, a TLS
// setting, a profile's tunnelTLS through a forward).
//
// Read by the two shapes it arrives in, whichever a driver passes on. A TLS
// server that does not speak HTTP answers with a TLS alert record, which
// Go's HTTP client reads as a "malformed HTTP response" quoting its first
// bytes, 0x15 0x03; and a server built on Go's own TLS listener — Vault,
// MinIO — answers 400 with "Client sent an HTTP request to an HTTPS server",
// which a driver quotes from the response it could not use. Words, because
// neither is typed by the time a driver hands it over: a plugin that reads
// the response itself hands its body in as the error's text.
func TLSExpected(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, tlsAlertQuoted) || strings.Contains(text, goTLSListenerPlainHTTP)
}

var (
	// tlsAlertQuoted is how Go's HTTP client quotes a response that opens on
	// a TLS alert record: content type 21 and a major version of 3.
	tlsAlertQuoted = "malformed HTTP response " + strings.TrimSuffix(strconv.Quote(string([]byte{0x15, 0x03})), `"`)
	// goTLSListenerPlainHTTP is net/http's answer to plain HTTP on a TLS port.
	goTLSListenerPlainHTTP = "Client sent an HTTP request to an HTTPS server"
)
