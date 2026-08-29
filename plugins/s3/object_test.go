package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// endpointOf turns an httptest.Server's URL into the host[:port] form the
// endpoint field takes — minio.New parses a bare authority, never a scheme
// (the scheme is the separate --tls bool).
func endpointOf(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	return strings.TrimPrefix(srv.URL, "http://")
}

// reqFor builds a resolved request for one capability against a fake
// endpoint, so a handler can be driven end to end without a live S3.
func reqFor(t *testing.T, capID, endpoint string, values map[string]any) plugin.Request {
	t.Helper()
	values["endpoint"] = endpoint
	values["access-key"] = "test"
	values["secret-key"] = "test"
	return req(t, capID, values)
}

// A regression test for a real bug live testing caught.
//
// minio-go's GetObject is lazy: it validates the names, returns a *Object,
// and does not issue the HTTP request until the first Read. So the error
// that actually says "no such key" arrives from io.ReadAll, not from
// GetObject — and the first draft wrapped exactly that in its own
// view.Errorf("s3.object.read", ...), turning every real S3 error on this
// path (a missing key, a denied read, expired credentials) into one
// undifferentiated "reading bucket/key: ..." with no code to switch on and
// no hint. Only running it against a real MinIO surfaced it; the unit tests
// on classify passed the whole time, because classify was never reached.
func TestAMissingObjectIsClassifiedThroughTheLazyRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A bare 404 with no XML body: minio-go falls back to the HTTP
		// status and builds NoSuchKey itself when an object name is in
		// play, which is what a real S3 404 on a GET decodes to.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := reqFor(t, "s3.object.get", endpointOf(t, srv), map[string]any{
		"bucket": "test-bucket", "key": "missing.txt",
	})
	_, err := runObjectGet(context.Background(), r)
	if err == nil {
		t.Fatal("a missing object returned no error")
	}
	verr := view.AsError(err, "x")
	if verr.Code != "s3.object.notfound" {
		t.Errorf("code = %q, want s3.object.notfound — the lazy read's error bypassed classify", verr.Code)
	}
	if verr.Hint == "" {
		t.Error("no hint")
	}
}

// The --out path reads through io.Copy rather than io.ReadAll and had the
// identical bug for the identical reason, so it gets its own case rather
// than trusting that one fix covered both.
func TestAMissingObjectIsClassifiedOnTheOutPathToo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "downloaded.txt")
	r := reqFor(t, "s3.object.get", endpointOf(t, srv), map[string]any{
		"bucket": "test-bucket", "key": "missing.txt", "out": out,
	})
	_, err := runObjectGet(context.Background(), r)
	if err == nil {
		t.Fatal("a missing object returned no error")
	}
	if verr := view.AsError(err, "x"); verr.Code != "s3.object.notfound" {
		t.Errorf("code = %q, want s3.object.notfound — io.Copy's error bypassed classify", verr.Code)
	}
}

// Every handler that reaches the network has to route its failure through
// classify, not just the ones that were remembered. This drives each one
// against a server that refuses everything and asserts the code is a real
// s3.* classification rather than whatever the library said — the check
// that would have caught the lazy-read bug in the first place, generalised
// so the next capability added here cannot quietly skip it.
func TestEveryHandlerClassifiesItsFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	endpoint := endpointOf(t, srv)

	// Every capability, with just enough inputs to reach the network.
	cases := []struct {
		capID  string
		run    plugin.Handler
		values map[string]any
	}{
		{"s3.overview", runOverview, map[string]any{}},
		{"s3.bucket.list", runBucketList, map[string]any{}},
		{"s3.policy.get", runPolicyGet, map[string]any{"bucket": "test-bucket"}},
		{"s3.object.list", runObjectList, map[string]any{"bucket": "test-bucket"}},
		{"s3.object.show", runObjectShow, map[string]any{"bucket": "test-bucket", "key": "test-key"}},
		{"s3.object.get", runObjectGet, map[string]any{"bucket": "test-bucket", "key": "test-key"}},
		{"s3.object.set", runObjectSet, map[string]any{"bucket": "test-bucket", "key": "test-key", "value": "v"}},
		{"s3.object.copy", runObjectCopy, map[string]any{"bucket": "test-bucket", "key": "test-key", "dest-key": "test-key-2"}},
		{"s3.object.rename", runObjectRename, map[string]any{"bucket": "test-bucket", "key": "test-key", "dest-key": "test-key-2"}},
		{"s3.object.rm", runObjectRemove, map[string]any{"bucket": "test-bucket", "key": "test-key"}},
	}
	for _, tc := range cases {
		t.Run(tc.capID, func(t *testing.T) {
			r := reqFor(t, tc.capID, endpoint, tc.values)
			_, err := tc.run(context.Background(), r)
			if err == nil {
				t.Fatal("a refusing server produced no error")
			}
			verr := view.AsError(err, "unclassified")
			if !strings.HasPrefix(verr.Code, "s3.") {
				t.Fatalf("code = %q, want an s3.* code — this failure never went through classify", verr.Code)
			}
			// The generic transport-level fallbacks mean the error reached
			// classify but matched nothing S3-specific, which for a server
			// answering 403 means the response was not classified as such.
			if verr.Code == "s3.conn.failed" {
				t.Errorf("code = %q: a 403 response classified as a connection failure", verr.Code)
			}
		})
	}
}
