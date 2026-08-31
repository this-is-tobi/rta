package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A tunnelled Vault's certificate is commonly signed by a CA nothing on this
// machine already trusts — an operator-generated root, a cluster-internal
// issuer — so this exercises the whole real path: connFields resolution,
// connect's TLS setup, and a real HTTP round trip through classify. Not a
// unit test of ConfigureTLS's own behaviour, which belongs to the vault
// module rather than to this plugin.
func TestCAFileTrustsATunneledServersOwnCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sealed":false,"initialized":true,"version":"1.0.0","storage_type":"raft"}`))
	}))
	defer srv.Close()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// Without ca-file: the server's own certificate is not signed by
	// anything this machine already trusts, so this is a real TLS failure
	// and has to be classified as one — not a timeout, not a refused
	// connection.
	_, err := runSealStatus(context.Background(),
		req(t, "vault.seal.status", map[string]any{"address": srv.URL}))
	if err == nil {
		t.Fatal("a self-signed server was trusted with no ca-file")
	}
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.tls.untrusted" {
		t.Fatalf("err = %v, want vault.tls.untrusted", err)
	}

	// With ca-file naming the server's own certificate: the handshake
	// succeeds and the call goes all the way through to a real answer.
	v, err := runSealStatus(context.Background(),
		req(t, "vault.seal.status", map[string]any{"address": srv.URL, "ca-file": caPath}))
	if err != nil {
		t.Fatalf("ca-file did not establish trust: %v", err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("view = %T", v)
	}
	found := false
	for _, p := range kv.Pairs {
		if p.Key == "storage" && p.Value == "raft" {
			found = true
		}
	}
	if !found {
		t.Errorf("the answer did not carry what the server sent: %+v", kv.Pairs)
	}
}

// ca-file is a path read on this machine, not a PEM blob a caller could hand
// over directly — a file that does not hold a certificate at all has to fail
// the same way, before any network call.
func TestCAFileRejectsAPathWithNoCertificate(t *testing.T) {
	notACert := filepath.Join(t.TempDir(), "not-a-cert.pem")
	if err := os.WriteFile(notACert, []byte("this is not PEM data"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, verr := connect(req(t, "vault.seal.status", map[string]any{
		"address": "https://127.0.0.1:1", "ca-file": notACert,
	}))
	if verr == nil {
		t.Fatal("a ca-file with no certificate in it was accepted")
	}
	if verr.Code != "vault.tls.ca.invalid" {
		t.Errorf("code = %s, want vault.tls.ca.invalid", verr.Code)
	}
}
