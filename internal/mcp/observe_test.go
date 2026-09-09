package mcp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// A verifier that accepts exactly one token, so the metrics wall can be shown
// to be a real one rather than a decoration.
func oneToken(token string) auth.TokenVerifier {
	return func(_ context.Context, got string, _ *http.Request) (*auth.TokenInfo, error) {
		if got != token {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{Scopes: []string{}}, nil
	}
}

func observe(t *testing.T, cfg ObserveConfig) http.Handler {
	t.Helper()
	return NewObserveHandler(cfg)
}

func get(t *testing.T, h http.Handler, path, bearer string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w.Result()
}

// Liveness answers whether the process is serving at all, so it cannot depend
// on anything that might be broken — including the store. A liveness probe
// that fails when a volume detaches asks Kubernetes to restart a process that
// would come back to the same detached volume.
func TestLivenessNeedsNoCredentialAndNoStore(t *testing.T) {
	h := observe(t, ObserveConfig{Ready: func() error { return errors.New("store is gone") }})
	if got := get(t, h, "/livez", "").StatusCode; got != http.StatusOK {
		t.Errorf("/livez = %d, want 200 even with an unusable store", got)
	}
}

// Readiness is the one that may say no: a detached volume or a full disk is
// alive but cannot serve, and that is the distinction that earns two endpoints
// instead of one.
func TestReadinessFailsWhenTheStoreIsUnusable(t *testing.T) {
	h := observe(t, ObserveConfig{Ready: func() error { return errors.New("data dir is read-only") }})
	res := get(t, h, "/readyz", "")
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d, want 503", res.StatusCode)
	}
	body := make([]byte, 512)
	n, _ := res.Body.Read(body)
	if !strings.Contains(string(body[:n]), "read-only") {
		t.Errorf("/readyz did not say what was wrong: %q", body[:n])
	}
}

func TestReadinessPassesWhenTheStoreIsUsable(t *testing.T) {
	h := observe(t, ObserveConfig{Ready: func() error { return nil }})
	if got := get(t, h, "/readyz", "").StatusCode; got != http.StatusOK {
		t.Errorf("/readyz = %d, want 200", got)
	}
}

// /healthz is the older combined name, kept because tooling asks for it by
// habit. It answers as readiness, which is the stricter of the two.
func TestHealthzAnswersAsReadiness(t *testing.T) {
	h := observe(t, ObserveConfig{Ready: func() error { return errors.New("nope") }})
	if got := get(t, h, "/healthz", "").StatusCode; got != http.StatusServiceUnavailable {
		t.Errorf("/healthz = %d, want 503 like /readyz", got)
	}
}

// **The wall this whole endpoint was argued about.** The counters name which
// agent called what and how often it was refused — a map of the machine's
// activity — so it sits behind the same bearer check as MCP itself rather than
// resting on which address somebody bound.
func TestMetricsRefusesAnUnauthenticatedCaller(t *testing.T) {
	h := observe(t, ObserveConfig{
		Verifier: oneToken("s3cret-token-long-enough"),
		Metrics:  func() (string, error) { return "rta_calls_total 1\n", nil },
	})
	if got := get(t, h, "/metrics", "").StatusCode; got == http.StatusOK {
		t.Error("/metrics served the record to a caller with no credential")
	}
	if got := get(t, h, "/metrics", "wrong-token-entirely").StatusCode; got == http.StatusOK {
		t.Error("/metrics served the record to a caller with the wrong credential")
	}
}

func TestMetricsServesTheExpositionToAnAuthenticatedCaller(t *testing.T) {
	h := observe(t, ObserveConfig{
		Verifier: oneToken("s3cret-token-long-enough"),
		Metrics:  func() (string, error) { return "rta_calls_total 1\n", nil },
	})
	res := get(t, h, "/metrics", "s3cret-token-long-enough")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("/metrics = %d, want 200", res.StatusCode)
	}
	body := make([]byte, 128)
	n, _ := res.Body.Read(body)
	if !strings.Contains(string(body[:n]), "rta_calls_total") {
		t.Errorf("/metrics body = %q", body[:n])
	}
	if ct := res.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want the Prometheus exposition type", ct)
	}
}

// A listener that answers four paths must not answer a fifth. This one is
// reachable by anything that can route to the address, so anything it does not
// mean to serve is a 404 rather than a surprise.
func TestTheObservationListenerServesNothingElse(t *testing.T) {
	h := observe(t, ObserveConfig{
		Verifier: oneToken("s3cret-token-long-enough"),
		Ready:    func() error { return nil },
		Metrics:  func() (string, error) { return "", nil },
	})
	for _, path := range []string{"/", "/debug/pprof/", "/operator/v1/call", "/mcp"} {
		if got := get(t, h, path, "s3cret-token-long-enough").StatusCode; got != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, got)
		}
	}
}

// With no Ready func there is nothing to consult, and saying "ready" would be
// asserting something nobody checked.
func TestReadinessWithNothingToCheckIsStillHonest(t *testing.T) {
	h := observe(t, ObserveConfig{})
	if got := get(t, h, "/readyz", "").StatusCode; got != http.StatusOK {
		t.Errorf("/readyz = %d, want 200 when the caller configured no check", got)
	}
}

// **The property the whole two-listener shape exists for.** The observation
// address answers a probe with no credential; the MCP address, running from
// the same Serve call, answers the same path with a refusal. If these ever
// collapse onto one listener this is the test that says so.
func TestTheMCPListenerGainsNoOpenPathsFromTheObservationOne(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	mcpLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	obsLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mcpAddr, obsAddr := mcpLn.Addr().String(), obsLn.Addr().String()

	server := NewServer(testRegistry(t), "test", Options{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, server, mcpLn, RemoteOptions{
			Verifier:        StaticTokenVerifier(map[string]string{"tok-a-0123456789abcdef": "alice"}),
			ObserveListener: obsLn,
			ObserveHandler: NewObserveHandler(ObserveConfig{
				Ready: func() error { return nil },
			}),
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve did not shut down cleanly: %v", err)
		}
	})

	res, err := http.Get("http://" + obsAddr + "/livez")
	if err != nil {
		t.Fatalf("observation listener unreachable: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Errorf("observation /livez = %d, want 200", res.StatusCode)
	}

	// The same path on the agent-facing address must not be open, and must not
	// be served at all.
	res2, err := http.Get("http://" + mcpAddr + "/livez")
	if err != nil {
		t.Fatalf("mcp listener unreachable: %v", err)
	}
	defer func() { _ = res2.Body.Close() }()
	if res2.StatusCode == http.StatusOK {
		t.Errorf("the MCP listener answered /livez with 200 — the listeners have collapsed")
	}
}
