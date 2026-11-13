package plugin

import (
	"crypto/ecdsa"
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
)

// bundle writes certs to one PEM file, in order, and returns its path.
func bundle(t *testing.T, certs ...*x509.Certificate) string {
	t.Helper()
	var out []byte
	for _, c := range certs {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// copyOf is a CA certificate for key under the subject issue gives every
// certificate, ending at ends: what a copy of a CA is once the cluster has
// renewed the CA in place with the key it had.
func copyOf(t *testing.T, key *ecdsa.PrivateKey, ends time.Time) *x509.Certificate {
	t.Helper()
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "db.internal"},
		NotBefore: ends.Add(-90 * 24 * time.Hour), NotAfter: ends, IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
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

// A copy of a CA whose every certificate has expired is refused before
// anything is dialled, by its date. The handshake said a certificate nothing
// here trusts, and told the operator to name the CA they had named. A bundle
// with one certificate still in its dates still vouches for what it signed.
func TestACACopyWhoseEveryCertificateHasExpiredIsRefusedBeforeDialling(t *testing.T) {
	stale, _ := issue(t, nil, nil, -time.Minute, true)
	req := asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, stale)})
	_, verr := redisService.TLS(req)
	if codeOf(verr) != "redis.tls.ca.expired" || verr.Input != "ca-file" ||
		!strings.Contains(verr.Message, stale.NotAfter.UTC().Format(time.DateOnly)) ||
		!strings.Contains(verr.Hint, "export the one in use again") {
		t.Fatalf("a stale copy: %+v, want redis.tls.ca.expired naming its date and the way out", verr)
	}

	current, _ := issue(t, nil, nil, time.Hour, true)
	req = asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, stale, current)})
	if _, verr := redisService.TLS(req); verr != nil {
		t.Errorf("a bundle that still holds a current CA was refused: %v", verr)
	}
}

// A CA a profile reads from where it is kept, for each call, is no copy: when
// what is kept there has expired, the refusal says so of the setting, never of
// a path that names nothing once the call returns, and sends the operator to
// whoever renews the CA rather than to export it again.
func TestACAReadForEachCallIsNotCalledACopy(t *testing.T) {
	stale, _ := issue(t, nil, nil, -time.Minute, true)
	path := bundle(t, stale)
	req := asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": path}).WithTemporary("ca-file")
	_, verr := redisService.TLS(req)
	if codeOf(verr) != "redis.tls.ca.expired" || strings.Contains(verr.Message, path) ||
		!strings.Contains(verr.Message, "every certificate in --ca-file has expired") ||
		!strings.Contains(verr.Hint, "read for each call from where the CA is kept") || strings.Contains(verr.Hint, "export") {
		t.Errorf("every certificate expired: %+v", verr)
	}

	signer, signerKey := issue(t, nil, nil, time.Hour, true)
	server, serverKey := issue(t, signer, signerKey, time.Hour, false)
	other, _ := issue(t, nil, nil, time.Hour, true)
	req = asked(map[string]any{"address": "127.0.0.1:6379",
		"ca-file": bundle(t, copyOf(t, signerKey, time.Now().Add(-time.Minute)), other)}).WithTemporary("ca-file")
	err := handshake(t, server, serverKey, planned(t, redisService, req))
	if verr := redisService.Refusal(req, err); codeOf(verr) != "redis.tls.ca.expired" ||
		!strings.Contains(verr.Hint, "read for each call from where the CA is kept") {
		t.Errorf("the CA that signed the server expired: %+v (%v)", verr, err)
	}
}

// With a CA named, a server it does not vouch for is never told to name a CA
// in ca-file, nor offered to be trusted on first use. A copy of the CA that
// signed the server's certificate, expired beside a CA that is current, is
// said as that, by its date: a CA renewed in place with its key signs
// certificates the old copy still matches, until the copy itself ends.
func TestAServerTheNamedCADoesNotVouchForIsToldWhyAndNotToNameACA(t *testing.T) {
	signer, signerKey := issue(t, nil, nil, time.Hour, true)
	server, serverKey := issue(t, signer, signerKey, time.Hour, false)
	other, _ := issue(t, nil, nil, time.Hour, true)

	req := asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, other)})
	err := handshake(t, server, serverKey, planned(t, redisService, req))
	verr := redisService.Refusal(req, err)
	if codeOf(verr) != "redis.tls.untrusted" || !strings.Contains(verr.Message, "does not vouch for") ||
		strings.Contains(verr.Hint, "belongs in") || verr.Presented != nil || verr.Input != "ca-file" {
		t.Errorf("a server another CA signed: %+v (%v)", verr, err)
	}

	stale := copyOf(t, signerKey, time.Now().Add(-time.Minute))
	req = asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, stale, other)})
	err = handshake(t, server, serverKey, planned(t, redisService, req))
	verr = redisService.Refusal(req, err)
	if codeOf(verr) != "redis.tls.ca.expired" || !strings.Contains(verr.Message, stale.NotAfter.UTC().Format(time.DateOnly)) ||
		!strings.Contains(verr.Hint, "export the one in use again") || verr.Presented != nil {
		t.Errorf("a server the expired copy signed: %+v (%v)", verr, err)
	}

	// Renewed in place: the server's certificate is good against the current
	// copy, which is what exporting it again gives.
	renewed := copyOf(t, signerKey, time.Now().Add(time.Hour))
	req = asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, renewed)})
	if err := handshake(t, server, serverKey, planned(t, redisService, req)); err != nil {
		t.Errorf("the copy exported again does not verify the server: %v", err)
	}

	// Only the copy that signed it is blamed: an expired CA under the same
	// name and another key is a CA that did not, and a current one that did,
	// beside a name that does not match, leaves the refusal the name's.
	_, strangerKey := issue(t, nil, nil, time.Hour, true)
	namesake := copyOf(t, strangerKey, time.Now().Add(-time.Minute))
	req = asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, namesake, other)})
	err = handshake(t, server, serverKey, planned(t, redisService, req))
	if verr := redisService.Refusal(req, err); codeOf(verr) != "redis.tls.untrusted" {
		t.Errorf("an expired CA that did not sign the certificate was blamed: %+v (%v)", verr, err)
	}
	req = asked(map[string]any{"address": "127.0.0.1:6379", "ca-file": bundle(t, renewed), "tls-server-name": "elsewhere.internal"})
	err = handshake(t, server, serverKey, planned(t, redisService, req))
	if verr := redisService.Refusal(req, err); codeOf(verr) != "redis.tls.name" {
		t.Errorf("a name that does not match beside the CA that signed it: %+v (%v)", verr, err)
	}
}
