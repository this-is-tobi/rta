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
