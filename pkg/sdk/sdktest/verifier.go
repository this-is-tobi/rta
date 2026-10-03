package sdktest

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Verdict is what a system's own verifier says of a certificate it refuses,
// in the words it says it, which Go passes on untyped.
type Verdict string

const (
	// VerdictRevoked is macOS's and iOS's: the issuer revoked the certificate.
	// plugin.CertRevoked reads it, and plugin.CertUntrusted never does.
	VerdictRevoked Verdict = "certificate is revoked"
	// VerdictNotTrusted is the system's refusal of a certificate whose issuer
	// nothing holds: plugin.CertUntrusted reads it.
	VerdictNotTrusted Verdict = "certificate is not trusted"
	// VerdictNotStandardsCompliant is Apple's answer to a certificate that
	// breaks a rule of its own, among them the validity period
	// plugin.CertPolicyHint reads.
	VerdictNotStandardsCompliant Verdict = "certificate is not standards compliant"
)

// VerifierSystem makes the certificate diagnostics in package plugin —
// CertUntrusted, CertRevoked, CertPolicyHint and Surface.CAHint — read a
// handshake's error as the verifier of goos would have worded it, until the
// test ends.
//
// **So a plugin's handling of a verdict only a Mac gives is tested where CI
// runs.** A revoked certificate and one Apple's policy refuses are answered
// by the system's verifier and by nothing else: a plugin's refusal for each,
// and the way round it must not offer, were exercised on a Mac or not at all.
// Pair it with SystemVerdict, which builds the error as that verifier words
// it:
//
//	sdktest.VerifierSystem(t, "darwin")
//	err := sdktest.SystemVerdict("db.internal", sdktest.VerdictRevoked)
//	// the plugin's handler, handed err, must refuse it in the system's
//	// words and offer no CA file or lesser mode.
//
// It sets what package plugin reads, so tests that use it do not run in
// parallel with each other, which would race over the one system.
func VerifierSystem(t testing.TB, goos string) {
	t.Helper()
	t.Cleanup(plugin.UseVerifierSystem(goos))
}

// SystemVerdict is the error a macOS or iOS handshake returns when the
// system's verifier refuses a certificate with verdict, as Go builds it: a
// *tls.CertificateVerificationError holding the certificates the server sent,
// leaf first, and the verdict in the system's description of the certificate,
// which opens on its name in curly quotes. name is that name, empty for a
// certificate that has none. Read it as the system's only under
// VerifierSystem(t, "darwin") or "ios".
func SystemVerdict(name string, verdict Verdict, sent ...*x509.Certificate) error {
	open, closing := string(rune(0x201c)), string(rune(0x201d))
	return &tls.CertificateVerificationError{
		UnverifiedCertificates: sent,
		Err:                    fmt.Errorf("x509: %s%s%s %s", open, name, closing, verdict),
	}
}
