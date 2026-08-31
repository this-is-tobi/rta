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

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A self-hosted S3-compatible endpoint behind a tunnel is commonly its own
// CA — an operator-generated root, a cluster-internal issuer — so this
// exercises the whole real path: connFields resolution, connect's TLS setup,
// and a real HTTPS round trip through classify. Not a unit test of
// minio.DefaultTransport's own behaviour, which belongs to that module.
func TestCAFileTrustsATunneledServersOwnCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Never actually reached by the "no ca-file" case — the handshake
		// itself fails first. Reached by the "with ca-file" case: any HTTP
		// response at all, including this refusal, proves the handshake
		// succeeded, which is the only thing this test is about.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	caPath := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(caPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	// s3's own endpoint field is host[:port] with no scheme (client.go's own
	// comment) — the server's real address, minus the https:// httptest hands back.
	endpoint := strings.TrimPrefix(srv.URL, "https://")

	// With tls: true but no ca-file: the server's own certificate is not
	// signed by anything this machine already trusts, so this is a real TLS
	// failure and has to be classified as one.
	_, err := runOverview(context.Background(), req(t, "s3.overview", map[string]any{
		"endpoint": endpoint, "tls": true,
	}))
	if err == nil {
		t.Fatal("a self-signed server was trusted with no ca-file")
	}
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "s3.tls.untrusted" {
		t.Fatalf("err = %v, want s3.tls.untrusted", err)
	}

	// With ca-file naming the server's own certificate: the handshake
	// succeeds and the call reaches the server for a real answer — a
	// refusal from the fake server, never a TLS trust failure.
	_, err = runOverview(context.Background(), req(t, "s3.overview", map[string]any{
		"endpoint": endpoint, "ca-file": caPath,
	}))
	if err == nil {
		t.Fatal("the fake server was expected to refuse the (unsigned-for-it) request")
	}
	if verr, ok := err.(*view.Error); ok && verr.Code == "s3.tls.untrusted" {
		t.Fatalf("ca-file did not establish trust: %v", verr)
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
	_, verr := connect(req(t, "s3.overview", map[string]any{
		"endpoint": "127.0.0.1:1", "ca-file": notACert,
	}))
	if verr == nil {
		t.Fatal("a ca-file with no certificate in it was accepted")
	}
	if verr.Code != "s3.tls.ca.invalid" {
		t.Errorf("code = %s, want s3.tls.ca.invalid", verr.Code)
	}
}
