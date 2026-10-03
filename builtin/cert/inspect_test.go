package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// inspectFile writes a certificate built from tmpl to a PEM file, runs
// cert.inspect on it, and returns the rows by key.
func inspectFile(t *testing.T, tmpl *x509.Certificate) map[string]string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if tmpl.SerialNumber == nil {
		tmpl.SerialNumber = big.NewInt(42)
	}
	if tmpl.NotAfter.IsZero() {
		tmpl.NotBefore, tmpl.NotAfter = time.Now().Add(-time.Hour), time.Now().Add(48*time.Hour)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	v, err := runInspect(context.Background(), req(map[string]any{"target": path}))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{}
	for _, p := range v.(view.KeyValue).Pairs {
		rows[p.Key] = p.Value
	}
	return rows
}

// A certificate lists every name it answers to. Only the DNS names were: one
// made for an address, which is what a service reached by IP, a kubelet or an
// etcd member carries, printed no names at all and read as valid for nothing.
func TestInspectListsEveryNameACertificateAnswersTo(t *testing.T) {
	rows := inspectFile(t, &x509.Certificate{
		Subject:        pkix.Name{CommonName: "svc"},
		DNSNames:       []string{"svc.internal", "*.svc.internal"},
		IPAddresses:    []net.IP{net.ParseIP("10.0.0.5"), net.ParseIP("::1")},
		EmailAddresses: []string{"ops@example.org"},
		URIs:           []*url.URL{{Scheme: "spiffe", Host: "example.org", Path: "/ns/prod/sa/api"}},
	})
	for key, want := range map[string]string{
		"dns-names":    "svc.internal, *.svc.internal",
		"ip-addresses": "10.0.0.5, ::1",
		"emails":       "ops@example.org",
		"uris":         "spiffe://example.org/ns/prod/sa/api",
	} {
		if rows[key] != want {
			t.Errorf("%s = %q, want %q", key, rows[key], want)
		}
	}

	rows = inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "bare"}})
	for _, key := range []string{"dns-names", "ip-addresses", "emails", "uris"} {
		if v, ok := rows[key]; ok {
			t.Errorf("a certificate with no %s has a row for them: %q", key, v)
		}
	}
}

// The serial is hexadecimal, a whole number of bytes: the spelling `openssl x509
// -serial`, a CRL, a CT log and a browser use. A 160-bit serial in decimal is
// 48 digits that match none of them.
func TestInspectSpellsTheSerialInHex(t *testing.T) {
	serial, _ := new(big.Int).SetString("637cb9bee3d623e2cfad17ec4e0680e8282c0c", 16)
	for _, c := range []struct {
		serial *big.Int
		want   string
	}{
		{serial, "637cb9bee3d623e2cfad17ec4e0680e8282c0c"},
		{big.NewInt(255), "ff"},
		{big.NewInt(15), "0f"},
		{big.NewInt(0x1234), "1234"},
	} {
		rows := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "s"}, SerialNumber: c.serial})
		if rows["serial"] != c.want {
			t.Errorf("serial %v = %q, want %q", c.serial, rows["serial"], c.want)
		}
	}
}
