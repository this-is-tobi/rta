package profile

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// caPEM is a self-signed CA's certificate, valid until ends.
func caPEM(t *testing.T, ends time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "pg-ca"},
		NotBefore: ends.Add(-90 * 24 * time.Hour), NotAfter: ends, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

const keyPEM = "-----BEGIN EC PRIVATE KEY-----\nMHcCAQEEIDoNotPrintThisKey\n-----END EC PRIVATE KEY-----\n"

// certCap is a connection plugin's capability with a CA setting declared to
// hold certificates, one of an older build that does not say so, and the
// username and password a profile's references commonly fill.
func certCap() plugin.Capability {
	return plugin.Capability{ID: "pg.query", Summary: "q", Safety: plugin.Read, Inputs: []plugin.Field{
		{Name: "ca-file", Type: plugin.String, Help: "CA", Local: true, Config: "ca-file", File: plugin.FileCertificates},
		{Name: "sslrootcert", Type: plugin.String, Help: "CA", Local: true, Config: "sslrootcert"},
		{Name: "user", Type: plugin.String, Help: "user", Local: true, Config: "user"},
		{Name: "password", Type: plugin.Secret, Help: "password", Local: true, EnvFallback: true},
	}}
}

func noEnvironment(string) (string, bool) { return "", false }

// A CA setting a profile maps onto a cluster's Secret is the Secret's
// certificates, read for this call and handed on as the content of a file,
// named by the reference that read it — and only the key the profile named,
// not the CA's private key the Secret keeps beside it.
func TestACASettingIsReadFromTheSecretForEachCall(t *testing.T) {
	ca := caPEM(t, time.Now().Add(30*24*time.Hour))
	fakeKubectl(t, "cat <<'JSON'\n"+secretJSON(map[string]string{"ca.crt": ca, "ca.key": keyPEM})+"\nJSON\n")
	conn := config.Connection{SecretsFrom: "lab/db", Secrets: map[string]string{"ca-file": "kube:pg-ca/ca.crt"}}
	got, verr := Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, nil)
	if verr != nil {
		t.Fatal(verr)
	}
	file, ok := got["ca-file"].(plugin.FileContent)
	if !ok || string(file.Bytes()) != ca || file.String() != "kube:pg-ca/ca.crt" {
		t.Fatalf("ca-file is %#v", got["ca-file"])
	}

	// From the store as well: the same mapping, another source. Only the
	// certificates are handed on, not what the entry held around them.
	read := func(string) (string, *view.Error) { return "subject=CN = pg-ca\n" + ca + "trailing words\n", nil }
	conn = config.Connection{Secrets: map[string]string{"ca-file": "kv:homelab/pg-ca"}}
	got, verr = Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, read)
	if file, ok := got["ca-file"].(plugin.FileContent); verr != nil || !ok || !bytes.Equal(file.Bytes(), []byte(ca)) ||
		file.String() != "kv:homelab/pg-ca" {
		t.Errorf("from the store: %#v, %+v", got["ca-file"], verr)
	}
}

// What a CA setting is never handed: a private key — mapped by mistake onto
// the Secret's other key — a block that is not a certificate, nothing at all,
// and certificates that have every one expired, refused by date. None of
// these refusals carries what the reference read.
func TestACASettingTakesCertificatesAlone(t *testing.T) {
	expired := time.Now().Add(-24 * time.Hour)
	for _, tc := range []struct {
		name, text, code, says string
	}{
		{"the CA's key", keyPEM, "core.profile.secret.key", "private key"},
		{"a certificate beside a key", caPEM(t, time.Now().Add(time.Hour)) + keyPEM, "core.profile.secret.key", "private key"},
		{"a revocation list", "-----BEGIN X509 CRL-----\nMIIB\n-----END X509 CRL-----\n", "core.profile.secret.certificate", "x509 crl"},
		{"a damaged certificate", "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n", "core.profile.secret.certificate", "does not parse"},
		{"no PEM", "pg-ca.crt", "core.profile.secret.certificate", "no PEM certificate"},
		{"an expired CA", caPEM(t, expired), "core.profile.secret.expired", expired.UTC().Format(time.DateOnly)},
	} {
		read := func(string) (string, *view.Error) { return tc.text, nil }
		conn := config.Connection{Secrets: map[string]string{"ca-file": "kv:homelab/pg-ca"}}
		_, verr := Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, read)
		if verr == nil || verr.Code != tc.code || !strings.Contains(verr.Message, tc.says) ||
			!strings.Contains(verr.Message, "kv:homelab/pg-ca") {
			t.Errorf("%s: %+v", tc.name, verr)
			continue
		}
		if strings.Contains(fmt.Sprint(verr.Message, verr.Hint), "DoNotPrintThisKey") {
			t.Errorf("%s: the refusal carries the key", tc.name)
		}
	}
}

// PEM a reference reads for any other setting that is no credential is
// refused, a key with a sentence of its own: such a setting is read as text or
// as a path, and the plugin's refusal of the open repeated the text. What is
// not PEM, and what lands in a credential, is handed on as it was.
func TestPEMIsRefusedOnASettingThatDoesNotNameAFileOfIt(t *testing.T) {
	ca := caPEM(t, time.Now().Add(time.Hour))
	for _, tc := range []struct {
		input, text, code string
	}{
		{"sslrootcert", ca, "core.profile.secret.pem"},
		{"sslrootcert", keyPEM, "core.profile.secret.key"},
		{"user", "-----BEGIN RSA PRIVATE KEY-----\nmangled", "core.profile.secret.key"},
	} {
		read := func(string) (string, *view.Error) { return tc.text, nil }
		conn := config.Connection{Secrets: map[string]string{tc.input: "kv:x"}}
		_, verr := Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, read)
		if verr == nil || verr.Code != tc.code || strings.Contains(verr.Message+verr.Hint, "DoNotPrintThisKey") {
			t.Errorf("%s holding %.20q: %+v", tc.input, tc.text, verr)
		}
	}
	if verr := func() *view.Error {
		_, verr := Fill(context.Background(), "ohmlab", config.Connection{Secrets: map[string]string{"sslrootcert": "kv:x"}},
			certCap(), nil, noEnvironment, func(string) (string, *view.Error) { return ca, nil })
		return verr
	}(); verr == nil || !strings.Contains(verr.Hint, "a pg that declares sslrootcert a file of certificates") {
		t.Errorf("the undeclared setting's hint: %+v", verr)
	}

	read := func(ref string) (string, *view.Error) {
		if ref == "user" {
			return "app", nil
		}
		return keyPEM, nil
	}
	conn := config.Connection{Secrets: map[string]string{"user": "kv:user", "password": "kv:password"}}
	got, verr := Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, read)
	if verr != nil || got["user"] != "app" || got["password"] != keyPEM {
		t.Errorf("got %v, %+v", got, verr)
	}
}

// Two settings a Secret fills that are both refused name the same one every
// run, the first in order, so a rerun is about the Secret and not about the
// order a map happened to give.
func TestTwoRefusedSettingsFromASecretNameTheFirst(t *testing.T) {
	fakeKubectl(t, "cat <<'JSON'\n"+secretJSON(map[string]string{"ca.crt": keyPEM, "root": keyPEM})+"\nJSON\n")
	conn := config.Connection{SecretsFrom: "lab/db",
		Secrets: map[string]string{"ca-file": "kube:pg-ca/ca.crt", "sslrootcert": "kube:pg-ca/root"}}
	for range 20 {
		_, verr := Fill(context.Background(), "ohmlab", conn, certCap(), nil, noEnvironment, nil)
		if verr == nil || !strings.Contains(verr.Message, " onto ca-file,") {
			t.Fatalf("named %+v", verr)
		}
	}
}
