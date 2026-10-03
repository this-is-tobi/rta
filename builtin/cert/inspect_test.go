package cert

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// selfIssued builds a certificate from tmpl, signed by its own fresh key, with
// the serial and the validity window a test did not care to choose.
func selfIssued(t *testing.T, tmpl *x509.Certificate) (der []byte, key *ecdsa.PrivateKey) {
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
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return der, key
}

// inspectFile writes a certificate built from tmpl to a PEM file, runs
// cert.inspect on it, and returns the rows by key.
func inspectFile(t *testing.T, tmpl *x509.Certificate) map[string]string {
	t.Helper()
	der, _ := selfIssued(t, tmpl)
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

// serveCert answers TLS on a loopback port with tmpl's certificate and returns
// the address.
func serveCert(t *testing.T, tmpl *x509.Certificate) string {
	t.Helper()
	der, key := selfIssued(t, tmpl)
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_ = conn.(*tls.Conn).Handshake()
				_ = conn.Close()
			}()
		}
	}()
	return ln.Addr().String()
}

// A certificate that is not valid yet fails every client's check as surely as
// an expired one, and read as a sound one for as long as its end date was far
// off: expires-in counted down to an end it was nowhere near, and the table
// said ok.
func TestACertificateNotValidYetIsSaidSo(t *testing.T) {
	future := &x509.Certificate{
		Subject:   pkix.Name{CommonName: "soon"},
		NotBefore: time.Now().Add(48 * time.Hour), NotAfter: time.Now().Add(100 * 24 * time.Hour),
	}
	rows := inspectFile(t, future)
	if got := rows["validity"]; !strings.HasPrefix(got, "not yet valid") || !strings.Contains(got, "begins in 1d") {
		t.Errorf("validity = %q, want it to say the certificate is not valid yet and for how long", got)
	}
	if v, ok := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "now"}})["validity"]; ok {
		t.Errorf("a certificate valid now has a validity row: %q", v)
	}

	v, err := runExpiry(context.Background(), req(map[string]any{
		"targets": []string{serveCert(t, future), serveCert(t, &x509.Certificate{
			Subject:   pkix.Name{CommonName: "ok"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(200 * 24 * time.Hour),
		})},
		"warn-days": 30, "timeout": 5,
	}))
	if err != nil {
		t.Fatal(err)
	}
	rowsOut := v.(view.Table).Rows
	if got := rowsOut[0][3]; !strings.HasPrefix(got, "INVALID") {
		t.Errorf("status of a certificate not valid yet = %q, want INVALID", got)
	}
	if got := rowsOut[1][3]; got != "ok" {
		t.Errorf("status of a sound certificate = %q, want ok", got)
	}
}

// The key is named by type and size. The signature algorithm beside it is the
// issuer's, so a certificate on a 1024-bit RSA key read as sound.
func TestInspectNamesTheKeyByTypeAndSize(t *testing.T) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		key  crypto.Signer
		want string
	}{{rsaKey, "RSA 2048"}, {edKey, "Ed25519"}, {p384, "ECDSA P-384"}} {
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "k"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, c.key.Public(), c.key)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if got := publicKeyOf(parsed); got != c.want {
			t.Errorf("publicKeyOf = %q, want %q", got, c.want)
		}
	}
	if got := inspectFile(t, &x509.Certificate{Subject: pkix.Name{CommonName: "k"}})["public-key"]; got != "ECDSA P-256" {
		t.Errorf("public-key row = %q, want ECDSA P-256", got)
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
