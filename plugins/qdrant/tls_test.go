package main

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A tunnelled Qdrant's certificate is commonly signed by a CA nothing on
// this machine already trusts — an operator-generated root, a
// cluster-internal issuer — so this exercises the whole real path: connFields
// resolution, httpClient's TLS setup, and a real HTTPS round trip through
// classify. Not a unit test of crypto/tls's own behaviour.
func TestCAFileTrustsATunneledServersOwnCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"collections":[]},"status":"ok","time":0}`))
	}))
	defer srv.Close()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := strings.TrimPrefix(srv.URL, "https://")

	// With tls: true but no ca-file: the server's own certificate is not
	// signed by anything this machine already trusts, so this is a real TLS
	// failure and has to be classified as one.
	_, err := collectionTable(context.Background(), req(t, "qdrant.collection.list", map[string]any{
		"endpoint": endpoint, "tls": true,
	}))
	if err == nil {
		t.Fatal("a self-signed server was trusted with no ca-file")
	}
	if err.Code != "qdrant.tls.untrusted" {
		t.Fatalf("code = %s, want qdrant.tls.untrusted", err.Code)
	}

	// With ca-file naming the server's own certificate: the handshake
	// succeeds and the call goes all the way through to a real answer.
	// ca-file alone turns TLS on — no --tls needed alongside it.
	table, err := collectionTable(context.Background(), req(t, "qdrant.collection.list", map[string]any{
		"endpoint": endpoint, "ca-file": caPath,
	}))
	if err != nil {
		t.Fatalf("ca-file did not establish trust: %v", err)
	}
	if table.Total != 0 {
		t.Errorf("got %d collections from an empty fake server: %+v", table.Total, table.Rows)
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
	verr := call(context.Background(), req(t, "qdrant.collection.list", map[string]any{
		"endpoint": "127.0.0.1:1", "ca-file": notACert,
	}), http.MethodGet, "/", nil, nil)
	if verr == nil {
		t.Fatal("a ca-file with no certificate in it was accepted")
	}
	if verr.Code != "qdrant.tls.ca.invalid" {
		t.Errorf("code = %s, want qdrant.tls.ca.invalid", verr.Code)
	}
}
