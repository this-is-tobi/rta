package cert

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
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

// cert.pem hands back the certificates themselves rather than a description
// of them, because behind a private CA the next step is always a file: a
// ConfigMap, a Dockerfile COPY, update-ca-certificates, a paste.
//
// So the thing to test is that what comes out parses — a PEM export that reads
// beautifully and cannot be loaded is worse than none, since it fails later,
// somewhere else, in whatever consumed it.

// parsePEM is the consumer's half: whatever this capability produced has to go
// back through the standard decoder and yield certificates.
func parsePEM(t *testing.T, body string) []*x509.Certificate {
	t.Helper()
	var out []*x509.Certificate
	for block, rest := pem.Decode([]byte(body)); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "CERTIFICATE" {
			t.Fatalf("block type = %q, want CERTIFICATE", block.Type)
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			t.Fatalf("a block did not parse as a certificate: %v", err)
		}
		out = append(out, c)
	}
	return out
}

func TestPEMRoundTripsThroughTheStandardDecoder(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runPEM(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Text).Body
	certs := parsePEM(t, body)
	if len(certs) == 0 {
		t.Fatalf("nothing was encoded:\n%s", body)
	}
	// The same bytes the host presented, not a re-serialization of a parse.
	live, _, err := loadCerts(context.Background(), addr, dialTimeout(req(nil)))
	if err != nil {
		t.Fatal(err)
	}
	if len(certs) != len(live) {
		t.Fatalf("encoded %d certificates, the host presented %d", len(certs), len(live))
	}
	if certs[0].SerialNumber.Cmp(live[0].SerialNumber) != 0 {
		t.Errorf("the encoded leaf is not the one presented")
	}
}

// A PEM file in, the same PEM out. The file branch matters as much as the dial
// one: converting a bundle somebody already has into a leaf-only or
// issuers-only file is the same job without a network.
func TestPEMReadsAFileAsWellAsAHost(t *testing.T) {
	_, pemPath := startTLS(t)
	v, err := runPEM(context.Background(), req(map[string]any{"target": pemPath}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(parsePEM(t, v.(view.Text).Body)); got != 1 {
		t.Fatalf("certificates = %d, want the one in the file", got)
	}
}

// --include leaf takes the end-entity certificate alone.
func TestPEMIncludeLeafPrintsOneCertificate(t *testing.T) {
	addr, _ := startTLS(t)
	v, err := runPEM(context.Background(), req(map[string]any{"target": addr, "include": "leaf"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(parsePEM(t, v.(view.Text).Body)); got != 1 {
		t.Fatalf("certificates = %d, want 1", got)
	}
}

// --include issuers is the private-CA case, and a self-signed host has none.
//
// Refused by name rather than answered with an empty file: a zero-byte
// ca-bundle is a deployment that fails somewhere else, later, with nothing
// pointing back here.
func TestPEMIncludeIssuersSaysSoWhenThereAreNone(t *testing.T) {
	addr, _ := startTLS(t)
	_, err := runPEM(context.Background(), req(map[string]any{"target": addr, "include": "issuers"}))
	if err == nil {
		t.Fatal("a leaf-only chain produced an issuers bundle")
	}
	if ve, ok := err.(*view.Error); !ok || ve.Code != "cert.chain.leafonly" {
		t.Fatalf("err = %v, want cert.chain.leafonly", err)
	}
}

// The issuers case with an actual issuer in the chain.
//
// The fixture is a generated certificate rather than a second startTLS: httptest
// hands every server the same built-in certificate, so a bundle of two of those
// cannot tell "dropped the leaf" from "dropped nothing".
func TestPEMIncludeIssuersDropsTheLeaf(t *testing.T) {
	_, leafPath := startTLS(t)
	leaf, err := os.ReadFile(leafPath)
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(t.TempDir(), "bundle.pem")
	if err := os.WriteFile(bundle, append(leaf, selfSigned(t, "Example Private CA")...), 0o644); err != nil {
		t.Fatal(err)
	}

	v, err := runPEM(context.Background(), req(map[string]any{"target": bundle, "include": "issuers"}))
	if err != nil {
		t.Fatal(err)
	}
	issuers := parsePEM(t, v.(view.Text).Body)
	if len(issuers) != 1 {
		t.Fatalf("certificates = %d, want the bundle minus its leaf", len(issuers))
	}
	if cn := issuers[0].Subject.CommonName; cn != "Example Private CA" {
		t.Errorf("issuer CN = %q, want the CA — the leaf is still in the bundle", cn)
	}
}

// selfSigned builds one certificate as PEM, for a fixture that needs two
// distinguishable ones.
func selfSigned(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(42),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// --out writes the file, and writes the same bytes it would have printed.
func TestPEMOutWritesExactlyWhatItWouldPrint(t *testing.T) {
	addr, _ := startTLS(t)
	printed, err := runPEM(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "nested", "chain.pem")
	v, err := runPEM(context.Background(), req(map[string]any{"target": addr, "out": out}))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.Contains(body, out) {
		t.Errorf("the confirmation does not name the file it wrote: %q", body)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("nothing was written: %v", err)
	}
	if string(written) != printed.(view.Text).Body {
		t.Error("the file and the printed form differ, so one of them is not the certificate")
	}
	// Public by construction, and the file exists to be read by something else.
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Errorf("mode = %o, want 0644", perm)
	}
}

// A dry run says what it would do and writes nothing, which is what makes
// --dry-run worth typing before a path somebody is not sure about.
func TestPEMDryRunWritesNothing(t *testing.T) {
	addr, _ := startTLS(t)
	out := filepath.Join(t.TempDir(), "chain.pem")
	r := plugin.NewRequest(map[string]any{"target": addr, "out": out}, true, false)
	v, err := runPEM(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.HasPrefix(body, "would write") {
		t.Errorf("dry run said %q", body)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("the dry run wrote %s", out)
	}
}

// --out is Local: it names a path on this machine, and which of this machine's
// files gets overwritten is not a question a remote caller answers, whatever
// it is being overwritten with.
func TestPEMOutIsLocal(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if c.ID != "cert.pem" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "out" && !f.Local {
				t.Error("cert.pem's --out is reachable from an MCP caller")
			}
		}
		return
	}
	t.Fatal("cert.pem is not in the plugin")
}

// **pem.Decode returns nil both when the input is exhausted and when a
// BEGIN line has no matching END.**
//
// So a bundle cut off mid-block — an interrupted download, a disk-full
// write, a half-finished paste — ended readPEM's loop exactly like a file
// that simply had no more certificates in it. `cert chain` then drew a
// short chain that looked complete, and `cert pem --out` wrote a trust
// bundle missing its root into whatever consumed it next, with nothing
// anywhere saying the file had been read only partly.
func TestABundleTruncatedMidBlockIsRefusedRatherThanShortened(t *testing.T) {
	dir := t.TempDir()
	whole := append(selfSigned(t, "Example Leaf"), selfSigned(t, "Example Private CA")...)

	// Cut inside the second block: everything up to its BEGIN line, plus a
	// few lines of its body, and no END.
	marker := []byte("-----BEGIN CERTIFICATE-----")
	second := bytes.LastIndex(whole, marker)
	if second <= 0 {
		t.Fatal("fixture does not hold two PEM blocks")
	}
	cut := whole[:second+len(marker)+40]

	path := filepath.Join(dir, "truncated.pem")
	if err := os.WriteFile(path, cut, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := readPEM(path)
	if err == nil {
		t.Fatal("a bundle ending inside a PEM block was read as a complete one")
	}
	verr := view.AsError(err, "")
	if verr.Code != "cert.file.truncated" {
		t.Errorf("code = %q, want cert.file.truncated", verr.Code)
	}

	// And the whole file still reads, so the guard is about truncation and
	// not about bundles.
	good := filepath.Join(dir, "whole.pem")
	if err := os.WriteFile(good, whole, 0o644); err != nil {
		t.Fatal(err)
	}
	certs, err := readPEM(good)
	if err != nil {
		t.Fatalf("an intact bundle was refused: %v", err)
	}
	if len(certs) != 2 {
		t.Errorf("read %d certificates from an intact two-certificate bundle", len(certs))
	}
}
