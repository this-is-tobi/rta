package http

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A request refused for a certificate Apple's verifier holds to its
// validity period is answered with that rule and its fix, where the reader
// was told to check the URL was reachable — which it was.
func TestARequestRefusedForItsCertificatesValidityPeriodSaysTheRule(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple's verifier is the one that holds a certificate to its validity period")
	}
	open, closing := string(rune(0x201c)), string(rune(0x201d))
	issued := time.Date(2025, time.March, 1, 0, 0, 0, 0, time.UTC)
	leaf := &x509.Certificate{NotBefore: issued, NotAfter: issued.Add(3650 * 24 * time.Hour)}
	err := &url.Error{Op: "Get", URL: "https://lab.internal", Err: &tls.CertificateVerificationError{
		UnverifiedCertificates: []*x509.Certificate{leaf},
		Err:                    fmt.Errorf("x509: %s", open+"lab.internal"+closing+" certificate is not standards compliant"),
	}}
	verr := requestFailed(plugin.SurfaceCLI, "GET", "https://lab.internal", err)
	if verr.Code != "http.request.failed" || !strings.Contains(verr.Hint, "at most 825 days") {
		t.Errorf("got %s %q, want the validity rule for a hint", verr.Code, verr.Hint)
	}

	other := requestFailed(plugin.SurfaceCLI, "GET", "https://lab.internal", errors.New("connection reset"))
	if !strings.Contains(other.Hint, "reachable") {
		t.Errorf("any other failure: hint %q, want the reachability hint", other.Hint)
	}
}

// A certificate that did not verify is the server's certificate, and the host
// is reachable. `http get https://expired.badssl.com` was told to check the URL
// was reachable and that --timeout extends the deadline; the client has no way
// round a certificate, so what helps is seeing the one the server presented,
// with a tool of this binary's own.
func TestARequestRefusedForAnUntrustedCertificatePointsAtWhatTheServerPresented(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://self-signed.example.org:8443/x", Err: &tls.CertificateVerificationError{
		Err: x509.UnknownAuthorityError{},
	}}
	verr := requestFailed(plugin.SurfaceCLI, "GET", "https://self-signed.example.org:8443/x", err)
	if strings.Contains(verr.Hint, "reachable") || strings.Contains(verr.Hint, "timeout") ||
		!strings.Contains(verr.Hint, "`rta cert chain self-signed.example.org:8443`") {
		t.Errorf("hint %q for a certificate that did not verify", verr.Hint)
	}
	// Over MCP the same pointer is the tool, with the host in it.
	mcp := requestFailed(plugin.SurfaceMCP, "GET", "https://self-signed.example.org/x", err)
	if !strings.Contains(mcp.Hint, "cert_chain") || !strings.Contains(mcp.Hint, "self-signed.example.org") {
		t.Errorf("hint %q over MCP", mcp.Hint)
	}
}

// A scheme the client does not speak is the URL's fault and not the network's.
// `http get ftp://host` was told to check the URL was reachable and that
// --timeout extends the deadline, neither of which has anything to do with a
// request that was never sent.
func TestARequestForASchemeItDoesNotSpeakSaysSo(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "ftp://example.com", Err: errors.New(`unsupported protocol scheme "ftp"`)}
	verr := requestFailed(plugin.SurfaceCLI, "GET", "ftp://example.com", err)
	if strings.Contains(verr.Hint, "reachable") || strings.Contains(verr.Hint, "timeout") ||
		!strings.Contains(verr.Hint, "http and https") {
		t.Errorf("hint %q for a scheme the client does not speak", verr.Hint)
	}
}
