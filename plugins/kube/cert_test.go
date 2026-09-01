package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

// selfSignedPEM builds a base64'd PEM the way a `kubernetes.io/tls` Secret's
// tls.crt field holds one, with the given expiry — everything certRow is
// meant to judge and nothing it is meant to read (there is no key here; this
// plugin never asks for one).
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
