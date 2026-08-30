package main

import (
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/minio/minio-go/v7"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/sdk/sdktest"
)

// sdktest is the definition of "a correct plugin" — no exemption for s3.
//
// It used to be called with no inputs, and every mutating capability here
// names a required bucket and key, so the suite drove *nothing*: twelve
// capabilities, zero runs, green. Behind that, s3.object.get truncated the
// operator's --out file under --dry-run, s3.object.set really uploaded, and
// s3.object.presign really minted a working bearer URL. The suite could see
// all three the moment it was given something to drive them with.
func TestConformance(t *testing.T) {
	sdktest.Check(t, Plugin(), sdktest.WithInputs(conformanceInputs))
}

// conformanceInputs points every mutating capability at a bucket and key that
// do not exist, on an endpoint nothing is listening on.
//
// The dead endpoint is the load-bearing half, copied from the built-ins' own
// fixture: a dry run that stops being dry fails here as a refused connection
// to 127.0.0.1:1 rather than as a request against somebody's real storage.
// Paths land inside dir, which is the directory the dry-run rule watches, so
// a write that should not have happened is a test failure rather than a file
// in a temp directory nobody looks at.
func conformanceInputs(dir string) map[string]map[string]any {
	conn := func(m map[string]any) map[string]any {
		m["endpoint"] = "127.0.0.1:1"
		m["access-key"], m["secret-key"] = "conformance", "conformance"
		return m
	}
	return map[string]map[string]any{
		"s3.object.get":  conn(map[string]any{"bucket": "conformance", "key": "some/key", "out": filepath.Join(dir, "got.bin")}),
		"s3.object.set":  conn(map[string]any{"bucket": "conformance", "key": "some/key", "value": "x"}),
		"s3.object.copy": conn(map[string]any{"bucket": "conformance", "key": "some/key", "dest-key": "some/copy"}),
		"s3.object.rename": conn(map[string]any{"bucket": "conformance", "key": "some/key",
			"dest-key": "some/renamed"}),
		"s3.object.rm":      conn(map[string]any{"bucket": "conformance", "key": "some/key"}),
		"s3.object.presign": conn(map[string]any{"bucket": "conformance", "key": "some/key"}),
		"s3.bucket.download": conn(map[string]any{"bucket": "conformance",
			"out": filepath.Join(dir, "bucket-copy")}),
	}
}

// req builds a resolved request the way the host would, against the named
// capability's own declared inputs (connFields included, via cap) — so
// these test the values a handler actually sees, matching plugins/vault's
// own req helper.
func req(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// Every classified failure has to say what to do next — the same bar
// plugins/pg and plugins/vault's own classify hold themselves to, against
// S3's error shapes.
func TestEveryClassifiedFailureNamesTheNextStep(t *testing.T) {
	r := req(t, "s3.overview", map[string]any{"endpoint": "s3.internal:9000"})
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"no such bucket", minio.ErrorResponse{Code: minio.NoSuchBucket, BucketName: "b"}, "s3.bucket.notfound"},
		{"no such key", minio.ErrorResponse{Code: minio.NoSuchKey, BucketName: "b", Key: "k"}, "s3.object.notfound"},
		{"no such policy", minio.ErrorResponse{Code: minio.NoSuchBucketPolicy, BucketName: "b"}, "s3.policy.notfound"},
		{"denied", minio.ErrorResponse{Code: minio.AccessDenied, Message: "nope"}, "s3.denied"},
		{"bad key id", minio.ErrorResponse{Code: minio.InvalidAccessKeyID}, "s3.auth.failed"},
		{"bad signature", minio.ErrorResponse{Code: minio.SignatureDoesNotMatch}, "s3.auth.failed"},
		{"bucket exists", minio.ErrorResponse{Code: minio.BucketAlreadyExists, BucketName: "b"}, "s3.bucket.exists"},
		{"other s3 error", minio.ErrorResponse{Code: "SomeOtherCode", Message: "m"}, "s3.request.failed"},
		{"refused", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "s3.conn.refused"},
		{"unknown host", &net.DNSError{Err: "no such host", Name: "s3.internal"}, "s3.host.unknown"},
		{"timed out", &url.Error{Op: "Get", URL: "http://x", Err: timeoutError{}}, "s3.conn.timeout"},
		{"anything else", errors.New("something unexpected"), "s3.conn.failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			verr := classify(tc.err, r)
			if verr.Code != tc.code {
				t.Errorf("code = %q, want %q", verr.Code, tc.code)
			}
			if verr.Hint == "" {
				t.Error("no hint")
			}
			if verr.Message == "" {
				t.Error("no message")
			}
		})
	}
}

// timeoutError satisfies net.Error's Timeout() so *url.Error.Timeout()
// (which asks its wrapped error) reports true, the same shape a real
// deadline exceeded error from the underlying transport has.
type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// Every capability that reaches an S3 endpoint reaches off the box, so cap
// must have forced NoPreview on all of them — the same property
// plugins/pg's and plugins/vault's own conformance tests pin, since it is
// what keeps the automatic dashboard from deciding, on its own, that a live
// endpoint is worth polling.
func TestEveryCapabilityIsNoPreview(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		if !c.NoPreview {
			t.Errorf("%s: NoPreview = false, want true — every capability here reaches off the box", c.ID)
		}
	}
}

// The capabilities designed as revealing content, granting
// access or overwriting/destroying it must actually declare NeedsGrant — a
// design note is not an enforcement mechanism, the struct field is.
func TestWriteAndDestructiveCapabilitiesNeedAGrant(t *testing.T) {
	want := map[string]bool{
		"s3.overview":    false,
		"s3.bucket.list": false,
		"s3.policy.get":  false,
		"s3.object.list": false,
		// Ungated for the reason a listing is ungated: this discloses exactly
		// what a caller could already have by walking s3.object.list one
		// --prefix at a time. Fewer round trips is not a wider permission —
		// the blast radius of a list of names and sizes is that same list.
		"s3.object.tree":    false,
		"s3.object.show":    false,
		"s3.object.get":     true,
		"s3.object.set":     true,
		"s3.object.copy":    true,
		"s3.object.rename":  true,
		"s3.object.rm":      false, // Destructive already implies a grant
		"s3.object.presign": true,
		// Refuses SurfaceMCP outright rather than taking a grant: a whole bucket
		// has no blast radius a grant could name. NeedsGrant stays unset for
		// keys.backup's reason — a grant that can never be exercised is an entry
		// in `grant list` that means nothing.
		"s3.bucket.download": false,
	}
	seen := map[string]bool{}
	for _, c := range Plugin().Capabilities {
		seen[c.ID] = true
		wantGrant, ok := want[c.ID]
		if !ok {
			t.Errorf("%s: not accounted for in this test's table", c.ID)
			continue
		}
		if c.NeedsGrant != wantGrant {
			t.Errorf("%s: NeedsGrant = %v, want %v", c.ID, c.NeedsGrant, wantGrant)
		}
		if c.ID == "s3.object.rm" && c.Safety != plugin.Destructive {
			t.Errorf("s3.object.rm: Safety = %s, want Destructive", c.Safety)
		}
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("%s: declared in this test's table but not in Plugin()", id)
		}
	}
}

func TestDestinationDefaultsTheBucketToTheSource(t *testing.T) {
	r := req(t, "s3.object.copy", map[string]any{"bucket": "src", "key": "k", "dest-key": "k2"})
	bucket, key := destination(r)
	if bucket != "src" || key != "k2" {
		t.Errorf("destination = (%q, %q), want (src, k2)", bucket, key)
	}
}

func TestDestinationHonorsAnExplicitDestBucket(t *testing.T) {
	r := req(t, "s3.object.copy", map[string]any{"bucket": "src", "key": "k", "dest-bucket": "dst", "dest-key": "k2"})
	bucket, key := destination(r)
	if bucket != "dst" || key != "k2" {
		t.Errorf("destination = (%q, %q), want (dst, k2)", bucket, key)
	}
}

func TestExpandHomeResolvesATildePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory in this environment")
	}
	if got := expandHome("~/x"); got != filepath.Join(home, "x") {
		t.Errorf("expandHome(~/x) = %q, want %q", got, filepath.Join(home, "x"))
	}
	if got := expandHome("/already/absolute"); got != "/already/absolute" {
		t.Errorf("expandHome left an absolute path alone incorrectly: %q", got)
	}
}
