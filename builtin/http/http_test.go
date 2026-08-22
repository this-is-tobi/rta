package http

import (
	"context"
	"encoding/json"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func req(values map[string]any) plugin.Request {
	if values["timeout"] == nil {
		values["timeout"] = 5
	}
	return plugin.NewRequest(values, false, false)
}

func pairsOf(t *testing.T, v view.View) map[string]string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	m := map[string]string{}
	for _, p := range kv.Pairs {
		m[p.Key] = p.Value
	}
	return m
}

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSafetyClasses(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		switch c.ID {
		case "http.get", "http.head":
			if c.Safety != plugin.Read {
				t.Errorf("%s should be read", c.ID)
			}
		default:
			if c.Safety != plugin.Write {
				t.Errorf("%s should be write", c.ID)
			}
		}
	}
}

func TestGetJSON(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL}))
	if err != nil {
		t.Fatal(err)
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "200") {
		t.Errorf("status = %q", pairs["status"])
	}
	if !strings.Contains(pairs["body"], "\"hello\": \"world\"") {
		t.Errorf("json not pretty-printed:\n%s", pairs["body"])
	}
	if pairs["time"] == "" {
		t.Error("missing timing")
	}
}

// A grant covers exactly the URL named (Scope: "url"), checked once by the
// MCP bridge before Run ever executes. Following a redirect automatically
// would let a call authorized for one destination actually reach whatever a
// 3xx response pointed at instead — no second grant check involved, and
// with no way for the operator who issued the grant to have known. This
// pins the fix: the redirect target's handler must never run, and its
// Location must be visible instead of silently chased.
func TestRedirectsAreNeverFollowedAutomatically(t *testing.T) {
	var forbiddenWasHit bool
	forbidden := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		forbiddenWasHit = true
		w.Write([]byte("secret metadata"))
	}))
	defer forbidden.Close()

	redirector := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		stdhttp.Redirect(w, r, forbidden.URL, stdhttp.StatusFound)
	}))
	defer redirector.Close()

	v, err := doRequest(context.Background(), "GET", req(map[string]any{"url": redirector.URL}))
	if err != nil {
		t.Fatal(err)
	}
	if forbiddenWasHit {
		t.Fatal("the redirect target was actually requested — a grant scoped to the redirector's URL was defeated")
	}
	pairs := pairsOf(t, v)
	if !strings.HasPrefix(pairs["status"], "302") {
		t.Errorf("status = %q, want the redirect response itself (302), not whatever it points at", pairs["status"])
	}
	if pairs["location (not followed)"] != forbidden.URL {
		t.Errorf("location (not followed) = %q, want %q", pairs["location (not followed)"], forbidden.URL)
	}
}

func TestAuthAndHeaders(t *testing.T) {
	var gotAuth, gotCustom string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
	}))
	defer srv.Close()

	_, err := doRequest(context.Background(), "GET", req(map[string]any{
		"url":    srv.URL,
		"bearer": "tok123",
		"header": []string{"X-Custom: yes"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer tok123" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotCustom != "yes" {
		t.Errorf("custom header = %q", gotCustom)
	}
}

func TestBasicAuth(t *testing.T) {
	var user, pass string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		user, pass, _ = r.BasicAuth()
	}))
	defer srv.Close()
	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": srv.URL, "basic": "alice:s3cret"}))
	if err != nil {
		t.Fatal(err)
	}
	if user != "alice" || pass != "s3cret" {
		t.Errorf("basic auth = %q:%q", user, pass)
	}
}

func TestPostBody(t *testing.T) {
	var received string
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		received = string(b)
	}))
	defer srv.Close()
	_, err := doRequest(context.Background(), "POST", req(map[string]any{"url": srv.URL, "data": `{"a":1}`}))
	if err != nil {
		t.Fatal(err)
	}
	if received != `{"a":1}` {
		t.Errorf("body = %q", received)
	}
}

func TestBadHeaderIsCoded(t *testing.T) {
	_, err := doRequest(context.Background(), "GET", req(map[string]any{
		"url": "http://127.0.0.1:1", "header": []string{"no-colon"},
	}))
	ve := view.AsError(err, "x")
	if ve.Code != "http.header.invalid" {
		t.Errorf("want http.header.invalid, got %+v", ve)
	}
}

func TestConnectionFailureIsCoded(t *testing.T) {
	_, err := doRequest(context.Background(), "GET", req(map[string]any{"url": "http://127.0.0.1:1"}))
	ve := view.AsError(err, "x")
	if ve.Code != "http.request.failed" || ve.Hint == "" {
		t.Errorf("want coded failure with hint, got %+v", ve)
	}
}

func TestBodyTruncation(t *testing.T) {
	big := strings.Repeat("x", 10000)
	out := formatBody([]byte(big), "text/plain")
	if len(out) >= 10000 || !strings.Contains(out, "more bytes") {
		t.Error("large body not truncated")
	}
}

// A --dry-run that sends the request anyway is worse than none at all: it
// reports what "would" happen after it has already happened.
func TestDryRunSendsNothing(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		hits++
		w.WriteHeader(stdhttp.StatusOK)
	}))
	defer srv.Close()

	for _, method := range []string{"post", "put", "delete"} {
		v, err := doRequest(context.Background(), strings.ToUpper(method),
			plugin.NewRequest(map[string]any{"url": srv.URL, "data": "payload", "timeout": 5}, true, true))
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		kv, ok := v.(view.KeyValue)
		if !ok {
			t.Fatalf("%s dry run = %s", method, view.TypeOf(v))
		}
		if kv.Pairs[0].Key != "dry run" {
			t.Errorf("%s dry run does not lead with what it did: %+v", method, kv.Pairs[0])
		}
	}
	if hits != 0 {
		t.Fatalf("dry runs reached the server %d times", hits)
	}

	// …and a real run still does.
	if _, err := doRequest(context.Background(), "POST",
		plugin.NewRequest(map[string]any{"url": srv.URL, "timeout": 5}, false, true)); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("a real request reached the server %d times", hits)
	}
}

// A dry run prints the request for inspection, which must not turn it into a
// way to print the token you were about to send.
func TestDryRunMasksCredentials(t *testing.T) {
	v, err := doRequest(context.Background(), "POST",
		plugin.NewRequest(map[string]any{"url": "https://example.com", "bearer": "s3cr3t", "timeout": 5}, true, true))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view.Envelope{View: view.Redact(v)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "s3cr3t") {
		t.Fatalf("the dry run echoed the credential: %s", raw)
	}
}
