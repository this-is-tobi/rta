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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
