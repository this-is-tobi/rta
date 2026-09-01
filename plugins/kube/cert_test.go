package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// selfSignedPEM builds a base64'd PEM the way a `kubernetes.io/tls` Secret's
// tls.crt field holds one, with the given expiry — everything certRow is
// meant to judge. No key is attached here because certRow has no use for one;
// that is a property of this helper, not of the wire, where the key does
// arrive (see the package comment in cert.go, and the test at the bottom of
// this file that pins what actually happens to it).
func selfSignedPEM(t *testing.T, cn string, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    notAfter.Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return base64.StdEncoding.EncodeToString(block)
}

func TestCertRow(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		pem := selfSignedPEM(t, "healthy.example.com", time.Now().Add(90*24*time.Hour))
		subject, _, status := certRow(pem)
		if subject != "healthy.example.com" || status != "ok" {
			t.Errorf("certRow = %q, %q, want healthy.example.com, ok", subject, status)
		}
	})

	t.Run("expiring soon", func(t *testing.T) {
		pem := selfSignedPEM(t, "soon.example.com", time.Now().Add(10*24*time.Hour))
		_, _, status := certRow(pem)
		if status != "expiring soon" {
			t.Errorf("certRow status = %q, want expiring soon", status)
		}
	})

	t.Run("expired", func(t *testing.T) {
		pem := selfSignedPEM(t, "gone.example.com", time.Now().Add(-24*time.Hour))
		_, _, status := certRow(pem)
		if status != "expired" {
			t.Errorf("certRow status = %q, want expired", status)
		}
	})

	t.Run("not base64", func(t *testing.T) {
		_, _, status := certRow("not valid base64!!!")
		if status != "tls.crt is not valid base64" {
			t.Errorf("certRow status = %q, want the base64 diagnosis", status)
		}
	})

	t.Run("not a certificate", func(t *testing.T) {
		garbage := base64.StdEncoding.EncodeToString([]byte("hello"))
		_, _, status := certRow(garbage)
		if status != "tls.crt has no parseable certificate" {
			t.Errorf("certRow status = %q, want the no-certificate diagnosis", status)
		}
	})
}

func TestCertPressure(t *testing.T) {
	secrets := list[tlsSecretItem]{Items: []tlsSecretItem{
		{
			Metadata: meta{Namespace: "prod", Name: "web-tls"},
			Data: struct {
				Cert string `json:"tls.crt"`
			}{Cert: selfSignedPEM(t, "web", time.Now().Add(-time.Hour))},
		},
		{
			Metadata: meta{Namespace: "prod", Name: "api-tls"},
			Data: struct {
				Cert string `json:"tls.crt"`
			}{Cert: selfSignedPEM(t, "api", time.Now().Add(5*24*time.Hour))},
		},
		{
			Metadata: meta{Namespace: "prod", Name: "fine-tls"},
			Data: struct {
				Cert string `json:"tls.crt"`
			}{Cert: selfSignedPEM(t, "fine", time.Now().Add(365*24*time.Hour))},
		},
	}}

	expired, soon := certPressure(secrets)
	if len(expired) != 1 || expired[0] != "prod/web-tls" {
		t.Errorf("expired = %v, want [prod/web-tls]", expired)
	}
	if len(soon) != 1 || soon[0] != "prod/api-tls" {
		t.Errorf("expiringSoon = %v, want [prod/api-tls]", soon)
	}
}

// The claim this replaces was that tls.key "is never requested" — false, and
// false in a way no test caught because the struct omitting the field looks
// like proof until you check the wire. There is no field projection for a
// Secret's data in the Kubernetes API, so the key always arrives; what this
// capability controls is only what happens next. That is the property pinned
// here: a Secret carrying private key material renders a certificate and
// nothing else, with the key surviving nowhere in the result.
func TestThePrivateKeyArrivesAndIsDroppedRatherThanRendered(t *testing.T) {
	const marker = "MARKER-PRIVATE-KEY-MUST-NEVER-BE-RENDERED"
	encodedKey := base64.StdEncoding.EncodeToString([]byte(marker))
	crt := selfSignedPEM(t, "kept.example.com", time.Now().Add(90*24*time.Hour))

	withFixtureKubectl(t, `{"items":[{"metadata":{"name":"web-tls","namespace":"prod"},"data":{`+
		`"tls.crt":"`+crt+`","tls.key":"`+encodedKey+`"}}]}`)

	v, err := runCertList(context.Background(), saReq(plugin.SurfaceCLI, false, map[string]any{
		"namespace": "prod",
	}))
	if err != nil {
		t.Fatal(err)
	}
	rendered := fmt.Sprintf("%#v", v)
	if !strings.Contains(rendered, "kept.example.com") {
		t.Fatalf("the certificate sharing the Secret with the key was not read:\n%s", rendered)
	}
	for _, forbidden := range []string{marker, encodedKey} {
		if strings.Contains(rendered, forbidden) {
			t.Errorf("private key material reached the rendered view:\n%s", rendered)
		}
	}
}
