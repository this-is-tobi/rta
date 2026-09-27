package cert

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// legacyTLS starts a TLS server that speaks nothing newer than TLS 1.1 and
// returns its host:port.
func legacyTLS(t *testing.T) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	srv.TLS = &tls.Config{
		MinVersion: tls.VersionTLS10, //nolint:gosec // the legacy host under inspection
		MaxVersion: tls.VersionTLS11,
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "https://")
}

func pairValue(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// cert kept Go's client floor of TLS 1.2, so a host speaking only 1.0 or 1.1
// failed the handshake and read as one that could not be reached: its
// certificate, the thing asked about, was never looked at. The handshake is
// an inspection, so it takes what the host speaks, and says which it was.
func TestALegacyHostIsInspectedAndItsProtocolNamed(t *testing.T) {
	addr := legacyTLS(t)

	v, err := runInspect(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatalf("cert.inspect: %v", err)
	}
	if got := pairValue(v.(view.KeyValue), "tls"); !strings.HasPrefix(got, "TLS 1.1") || !strings.Contains(got, "deprecated") {
		t.Errorf("cert.inspect tls = %q, want TLS 1.1 named as deprecated", got)
	}

	if _, err := runChain(context.Background(), req(map[string]any{"target": addr})); err != nil {
		t.Errorf("cert.chain: %v", err)
	}
	if _, err := runPEM(context.Background(), req(map[string]any{"target": addr})); err != nil {
		t.Errorf("cert.pem: %v", err)
	}

	v, err = runTLS(context.Background(), req(map[string]any{"target": addr}))
	if err != nil {
		t.Fatalf("cert.tls: %v", err)
	}
	if got := pairValue(v.(view.KeyValue), "version"); !strings.HasPrefix(got, "TLS 1.1") || !strings.Contains(got, "deprecated") {
		t.Errorf("cert.tls version = %q, want TLS 1.1 named as deprecated, as cert.inspect names it", got)
	}
}

// cert.expiry grades, so a deprecated protocol is graded there as the
// weakness it is, beside whatever the certificate's dates say.
func TestExpiryGradesALegacyProtocolAsAWeakness(t *testing.T) {
	legacy := legacyTLS(t)
	current, _ := startTLS(t)
	v, err := runExpiry(context.Background(), req(map[string]any{"targets": []string{legacy, current}, "warn-days": 30}))
	if err != nil {
		t.Fatal(err)
	}
	rows := v.(view.Table).Rows
	if got := rows[0][3]; !strings.HasPrefix(got, "WARN") || !strings.Contains(got, "TLS 1.1") {
		t.Errorf("legacy host status = %q, want a warning naming TLS 1.1", got)
	}
	if got := rows[1][3]; got != "ok" {
		t.Errorf("current host status = %q, want ok", got)
	}
}
