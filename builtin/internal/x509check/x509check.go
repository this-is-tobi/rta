// Package x509check holds the certificate judgements the cert and audit
// built-ins both make, because they were each making them separately.
// cert.verify and audit.verifyChain were the same x509 routine with
// different sentinel strings, and the expiry window was 15 days in the audit
// against `cert expiry`'s default of 30 — so the same certificate, 20 days
// from expiry, came back "ok" from `rta audit web` and "WARN <30d" from
// `rta cert expiry`, on the same host, in the same minute. One
// implementation and one default, read by both.
package x509check

import (
	"crypto/tls"
	"crypto/x509"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// DefaultWarnDays is how close to expiry a certificate has to be before
// saying so is useful. Thirty days is the window a person can act inside: an
// ACME client renews at 30 days out, so a certificate still inside the
// window has had its automation run and visibly not work, which is the
// finding worth reporting.
const DefaultWarnDays = 30

// Chain reports why the presented chain does not validate for host, or ""
// when it does. certs is leaf-first, the order a TLS peer chain arrives in.
//
// An empty host verifies the signature path alone. That is the PEM-file
// case, not laxness: a certificate sitting on disk has no name it was served
// under, and deriving one from the file's path would fail every one of them
// for a reason that has nothing to do with the certificate.
func Chain(certs []*x509.Certificate, host string) string {
	if len(certs) == 0 {
		return "no certificate presented"
	}
	intermediates := x509.NewCertPool()
	for _, c := range certs[1:] {
		intermediates.AddCert(c)
	}
	// DNSName is left empty rather than skipped for the host == "" case:
	// x509.Verify only checks the hostname when DNSName is non-empty, so the
	// two spellings are the same check.
	opts := x509.VerifyOptions{Intermediates: intermediates, DNSName: host}
	if _, err := certs[0].Verify(opts); err != nil {
		return invalid(certs, err)
	}
	return ""
}

// invalid is the reason Chain gives for a chain the system refused, with the
// fix beside it where the refusal is one of Apple's own rules the
// certificate breaks (plugin.CertPolicyHint): a ten-year self-signed
// certificate, the usual one for a lab or a cluster, was "not standards
// compliant" and nothing more. Verify's error is handed over as a handshake
// carries it, since the hint reads the certificate the server sent.
func invalid(certs []*x509.Certificate, err error) string {
	reason := "INVALID: " + err.Error()
	sent := &tls.CertificateVerificationError{UnverifiedCertificates: certs, Err: err}
	if hint := plugin.CertPolicyHint(sent); hint != "" {
		reason += " — " + hint
	}
	return reason
}

// Expiring reports whether notAfter falls inside the last warnDays of a
// certificate's life. An already-expired certificate is the louder finding
// and both callers test for that first, so this reports true for one rather
// than pretending it is a third state nobody asked about.
func Expiring(notAfter time.Time, warnDays int) bool {
	return time.Until(notAfter) < time.Duration(warnDays)*24*time.Hour
}
