package plugin

import (
	"crypto/x509"
	"fmt"
	"slices"
	"strconv"
	"strings"
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
