package cert

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// issued signs a certificate for cn ending after `lasts`, under parent when
// there is one and under itself otherwise.
func issued(t *testing.T, cn string, lasts time.Duration, parent *x509.Certificate, parentKey *ecdsa.PrivateKey) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<30))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(lasts),
		IsCA: parent == nil || cn != "leaf", BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	if parent == nil {
		parent, parentKey = tmpl, key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

// chainOf is a leaf under an intermediate under a root, each ending after
// the span given, leaf first as a host presents it.
func chainOf(t *testing.T, leaf, inter, root time.Duration) ([]*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	r, rk := issued(t, "root", root, nil, nil)
	i, ik := issued(t, "inter", inter, r, rk)
	l, lk := issued(t, "leaf", leaf, i, ik)
	return []*x509.Certificate{l, i, r}, lk
}

func serveChain(t *testing.T, chain []*x509.Certificate, key *ecdsa.PrivateKey) string {
	t.Helper()
	var raw [][]byte
	for _, c := range chain {
		raw = append(raw, c.Raw)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: raw, PrivateKey: key}},
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

func bundleFile(t *testing.T, chain []*x509.Certificate) string {
	t.Helper()
	var b []byte
	for _, c := range chain {
		b = append(b, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	path := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func expiryRows(t *testing.T, r plugin.Request) [][]string {
	t.Helper()
	v, err := runExpiry(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return v.(view.Table).Rows
}

const day = 24 * time.Hour

// cert.expiry graded the leaf alone, so an intermediate two days from its end
// under a leaf with eighty read ok, with cert.chain the only place that showed
// it. The soonest end among the leaf and its intermediates sets the row, and
// the status names it when it is not the leaf. A root the host sent along is
// the client's store's to judge and does not grade the host.
func TestExpiryGradesTheChainNotTheLeaf(t *testing.T) {
	chain, key := chainOf(t, 80*day, 2*day, 1*day)
	row := expiryRows(t, req(map[string]any{
		"targets": []string{serveChain(t, chain, key)}, "warn-days": 30, "timeout": 5,
	}))[0]
	if !strings.HasPrefix(row[3], "WARN <30d") || !strings.Contains(row[3], "intermediate inter") {
		t.Errorf("status = %q, want WARN naming the intermediate", row[3])
	}
	if want := chain[1].NotAfter.Format("2006-01-02"); row[1] != want {
		t.Errorf("expires = %q, want the intermediate's %q", row[1], want)
	}

	healthy, hkey := chainOf(t, 80*day, 400*day, 1*day)
	if row := expiryRows(t, req(map[string]any{
		"targets": []string{serveChain(t, healthy, hkey)}, "warn-days": 30, "timeout": 5,
	}))[0]; row[3] != "ok" {
		t.Errorf("a root ending tomorrow graded the host: status = %q, want ok", row[3])
	}

	gone, gkey := chainOf(t, 80*day, -day, 400*day)
	if row := expiryRows(t, req(map[string]any{
		"targets": []string{serveChain(t, gone, gkey)}, "warn-days": 30, "timeout": 5,
	}))[0]; !strings.HasPrefix(row[3], "EXPIRED") || !strings.Contains(row[3], "intermediate inter") {
		t.Errorf("status = %q, want EXPIRED naming the intermediate", row[3])
	}
}

// At a terminal a target that is a file is read, so "which of my certificate
// files end soon" is one command. Over MCP it is still dialled and refused as
// the file path it is: targets is a StringSlice the path gate cannot hook, and
// reading a path through it would answer for anywhere on the machine.
func TestExpiryReadsCertificateFilesAtATerminalOnly(t *testing.T) {
	chain, _ := chainOf(t, 80*day, 2*day, 400*day)
	path := bundleFile(t, chain)
	values := map[string]any{"targets": []string{path}, "warn-days": 30, "timeout": 2}

	row := expiryRows(t, req(values))[0]
	if !strings.HasPrefix(row[3], "WARN <30d") || !strings.Contains(row[3], "intermediate inter") {
		t.Errorf("a file at a terminal: status = %q, want the bundle graded", row[3])
	}
	row = expiryRows(t, req(values).WithSurface(plugin.SurfaceMCP))[0]
	if !strings.Contains(row[3], "file path") || row[1] != "-" {
		t.Errorf("a file over MCP: %v, want it refused as a file path and not read", row)
	}
}
