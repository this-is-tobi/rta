package net

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
	stdnet "net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// peer is a TLS server taking one connection: its port, its certificate as a
// PEM file (the CA a send can name, being self-signed), and what it read
// after a handshake that completed, nil when none did.
type peer struct {
	port   int
	caFile string
	got    chan []byte
}

// tlsPeer starts a peer presenting a self-signed certificate valid for names,
// each a DNS name or an IP address, until notAfter. It answers +PONG to the
// first thing it reads.
func tlsPeer(t *testing.T, notAfter time.Time, names ...string) peer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: names[0]},
		NotBefore:             notAfter.Add(-48 * time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, n := range names {
		if ip := stdnet.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(t.TempDir(), "peer.pem")
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			got <- nil
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		if err := conn.(*tls.Conn).Handshake(); err != nil {
			got <- nil
			return
		}
		buf := make([]byte, 256)
		n, _ := conn.Read(buf)
		got <- buf[:n]
		_, _ = conn.Write([]byte("+PONG\r\n"))
	}()
	return peer{port: ln.Addr().(*stdnet.TCPAddr).Port, caFile: caFile, got: got}
}

// received is what p read after a completed handshake.
func (p peer) received(t *testing.T) []byte {
	t.Helper()
	select {
	case b := <-p.got:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("the peer never finished with its connection")
		return nil
	}
}

// sendPing sends PING to 127.0.0.1 on port, on surface, with values added.
func sendPing(surface plugin.Surface, port int, dryRun bool, values map[string]any) (view.View, error) {
	in := map[string]any{"host": "127.0.0.1", "port": port, "data": `PING\r\n`, "timeout": "2s", "wait": "1s"}
	for k, v := range values {
		in[k] = v
	}
	return runSend(context.Background(), plugin.NewRequest(in, dryRun, false).WithSurface(surface))
}

// A send over TLS wrote the caller's bytes, a password in an AUTH line among
// them, to whatever answered the handshake: the peer's certificate went
// unchecked. One nothing here trusts is now refused before a byte is
// written, and the reader is told where its CA goes.
func TestASendOverTLSWritesNothingToAPeerItCannotCheck(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(time.Hour), "127.0.0.1")
	_, err := sendPing(plugin.SurfaceCLI, p.port, false, map[string]any{"tls": true})
	ve := view.AsError(err, "")
	if ve == nil || ve.Code != "net.tls.untrusted" {
		t.Fatalf("err = %v, want net.tls.untrusted", err)
	}
	if got := p.received(t); got != nil {
		t.Fatalf("the peer was sent %q", got)
	}
	for _, want := range []string{"--ca-file", "rta cert pem 127.0.0.1:"} {
		if !strings.Contains(ve.Hint, want) {
			t.Errorf("hint %q does not name %s", ve.Hint, want)
		}
	}
}

// An agent cannot name a CA: ca-file is Local, so the hint it reads says
// whose the fix is rather than naming a flag it has no way to give.
func TestAnAgentIsToldTheTrustStoreIsTheOperators(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(time.Hour), "127.0.0.1")
	_, err := sendPing(plugin.SurfaceMCP, p.port, false, map[string]any{"tls": true})
	ve := view.AsError(err, "")
	if ve == nil || ve.Code != "net.tls.untrusted" {
		t.Fatalf("err = %v, want net.tls.untrusted", err)
	}
	if !strings.Contains(ve.Hint, "trust store") || strings.Contains(ve.Hint, "--ca-file") || strings.Contains(ve.Hint, "cert pem") {
		t.Errorf("hint to an agent = %q", ve.Hint)
	}
}

// A CA the caller names is the one the peer is checked against, and a CA
// alone asks for TLS: a payload meant for a checked peer never leaves in the
// clear because `tls` was not typed beside it.
func TestASendTrustsTheCAItIsGivenAndACAAloneAsksForTLS(t *testing.T) {
	for name, values := range map[string]map[string]any{
		"with tls":      {"tls": true},
		"ca-file alone": {},
	} {
		t.Run(name, func(t *testing.T) {
			p := tlsPeer(t, time.Now().Add(time.Hour), "127.0.0.1")
			values["ca-file"] = p.caFile
			v, err := sendPing(plugin.SurfaceCLI, p.port, false, values)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(p.received(t)); got != "PING\r\n" {
				t.Fatalf("the peer read %q", got)
			}
			s := v.(view.Sections)
			var version string
			for _, pair := range s.Items[0].View.(view.KeyValue).Pairs {
				if pair.Key == "tls" {
					version = pair.Value
				}
			}
			if version == "" || !strings.Contains(s.Items[1].View.(view.Text).Body, "+PONG") {
				t.Fatalf("tls %q, response %q", version, s.Items[1].View.(view.Text).Body)
			}
		})
	}
}

// A certificate for another name is refused under its own code, saying which
// names it holds, and tls-server-name checks it for one of them while the
// call still reaches the address it named.
func TestASendRefusesACertificateForAnotherNameUntilItIsCheckedForIt(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(time.Hour), "db.internal")
	_, err := sendPing(plugin.SurfaceCLI, p.port, false, map[string]any{"ca-file": p.caFile})
	ve := view.AsError(err, "")
	if ve == nil || ve.Code != "net.tls.name" || !strings.Contains(ve.Message, "db.internal") {
		t.Fatalf("err = %v, want net.tls.name naming db.internal", err)
	}
	if !strings.Contains(ve.Hint, "--tls-server-name") {
		t.Errorf("hint %q does not name --tls-server-name", ve.Hint)
	}
	if got := p.received(t); got != nil {
		t.Fatalf("the peer was sent %q", got)
	}

	p = tlsPeer(t, time.Now().Add(time.Hour), "db.internal")
	if _, err := sendPing(plugin.SurfaceCLI, p.port, false, map[string]any{
		"ca-file": p.caFile, "tls-server-name": "db.internal",
	}); err != nil {
		t.Fatal(err)
	}
	if got := string(p.received(t)); got != "PING\r\n" {
		t.Fatalf("the peer read %q", got)
	}
}

// Any other verdict on the certificate, one not valid before tomorrow here, is
// refused with the verifier's words, and the payload stays unwritten.
func TestASendRefusesACertificateNotYetValid(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(72*time.Hour), "127.0.0.1")
	_, err := sendPing(plugin.SurfaceCLI, p.port, false, map[string]any{"ca-file": p.caFile})
	ve := view.AsError(err, "")
	if ve == nil || ve.Code != "net.tls.rejected" || !strings.Contains(ve.Message, "not yet valid") {
		t.Fatalf("err = %v, want net.tls.rejected saying it is not yet valid", err)
	}
	if got := p.received(t); got != nil {
		t.Fatalf("the peer was sent %q", got)
	}
}

// A CA file whose every certificate has expired, the peer's own self-signed
// one here, is refused before anything is dialled: no chain anchored at it
// verifies, and the refusal says so by its date.
func TestASendRefusesACAFileWhoseEveryCertificateHasExpired(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(-time.Hour), "127.0.0.1")
	_, err := sendPing(plugin.SurfaceCLI, p.port, false, map[string]any{"ca-file": p.caFile})
	ve := view.AsError(err, "")
	if ve == nil || ve.Code != "net.tls.ca.expired" || ve.Input != "ca-file" {
		t.Fatalf("err = %v, want net.tls.ca.expired naming ca-file", err)
	}
	select {
	case got := <-p.got:
		t.Fatalf("the peer was dialled (it read %q)", got)
	case <-time.After(100 * time.Millisecond):
	}
}

// A dry run says the send would go over TLS when a CA alone asks for it, and
// refuses a CA file that does not read, as the send itself would: everything
// short of the network is resolved, so the report is the real one.
func TestADryRunReadsTheCAAndSaysTheSendIsOverTLS(t *testing.T) {
	p := tlsPeer(t, time.Now().Add(time.Hour), "127.0.0.1")
	v, err := sendPing(plugin.SurfaceCLI, p.port, true, map[string]any{"ca-file": p.caFile})
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, "over TLS") {
		t.Fatalf("dry run = %q", body)
	}
	_, err = sendPing(plugin.SurfaceCLI, p.port, true, map[string]any{"ca-file": filepath.Join(t.TempDir(), "none.pem")})
	if ve := view.AsError(err, ""); ve == nil || ve.Code != "net.tls.ca.unreadable" || ve.Input != "ca-file" {
		t.Fatalf("err = %v, want net.tls.ca.unreadable on ca-file", err)
	}
}
