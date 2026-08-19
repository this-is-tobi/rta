package x509check

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"strings"
	"testing"
	"time"
)

// selfSigned mints a certificate valid for host, expiring at notAfter.
func selfSigned(t *testing.T, host string, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// "" is the sentinel for a chain that validates, so an empty chain must never
// produce one: a caller that asked about a host presenting no certificate at
// all would be told the certificate is fine.
func TestChainRejectsAnEmptyChainWithAReason(t *testing.T) {
	if got := Chain(nil, "example.com"); got == "" {
		t.Error("an empty chain verified")
	}
	if got := Chain([]*x509.Certificate{}, ""); got == "" {
		t.Error("an empty chain verified with no host either")
	}
}

// The reason is shown to a person and has to say what went wrong, not just
// that something did — "INVALID" on its own sends the reader back to openssl.
func TestChainReasonCarriesTheVerificationError(t *testing.T) {
	c := selfSigned(t, "rta-test.invalid", time.Now().Add(365*24*time.Hour))
	got := Chain([]*x509.Certificate{c}, "rta-test.invalid")
	if !strings.HasPrefix(got, "INVALID: ") {
		t.Fatalf("self-signed chain reported as %q", got)
	}
	if strings.TrimPrefix(got, "INVALID: ") == "" {
		t.Error("verdict carries no reason")
	}
}

// The two callers merged into this one had different windows — 30 days in
// `cert expiry`, a private 15 inside the audit — so the boundary is worth
// pinning rather than left to whichever caller is read first.
func TestExpiringCoversTheLastWarnDaysOfValidity(t *testing.T) {
	tests := []struct {
		name     string
		notAfter time.Time
		warnDays int
		want     bool
	}{
		{"well inside validity", time.Now().Add(90 * 24 * time.Hour), 30, false},
		{"just outside the window", time.Now().Add(31 * 24 * time.Hour), 30, false},
		{"just inside the window", time.Now().Add(29 * 24 * time.Hour), 30, true},
		{"already expired", time.Now().Add(-24 * time.Hour), 30, true},
		{"twenty days out, the case the two callers disagreed on", time.Now().Add(20 * 24 * time.Hour), DefaultWarnDays, true},
		{"warnings switched off", time.Now().Add(time.Hour), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Expiring(tt.notAfter, tt.warnDays); got != tt.want {
				t.Errorf("Expiring(%s away, %d) = %v, want %v",
					time.Until(tt.notAfter).Round(time.Hour), tt.warnDays, got, tt.want)
			}
		})
	}
}
