package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The assertion here is on the wire, not on the filesystem. The conformance
// suite watches a directory, which cannot see a request that leaves the
// machine — and every defect this file pins was a request that left the
// machine. recordingS3 (complete_test.go) already records every request line,
// so it is the recorder; the canned body is irrelevant because under
// --dry-run nothing is supposed to ask for it.

// An empty bucket, so a preview that legitimately lists gets a parseable
// answer and still has nothing to write.
const emptyListingXML = `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Name>test-bucket</Name><IsTruncated>false</IsTruncated>
</ListBucketResult>`

// dryReq is reqFor with DryRun set — the only difference that matters here.
func dryReq(t *testing.T, capID, endpoint string, values map[string]any) plugin.Request {
	t.Helper()
	values["endpoint"] = endpoint
	values["access-key"] = "test"
	values["secret-key"] = "test"
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), true, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

// **Every mutating capability, driven under --dry-run, against a server that
// fails the test if it is touched.**
//
// Three of these shipped performing the act they promised to describe:
// s3.object.set really uploaded, s3.object.presign really minted a signed
// bearer URL valid for up to seven days, and s3.object.get truncated the
// operator's --out file before the first byte arrived. Table-driven and
// derived from the declaration, so a capability added next year is covered
// the day it lands rather than the day somebody remembers this file.
func TestNoMutatingCapabilityActsUnderDryRun(t *testing.T) {
	// A preview is allowed to *read* when reading is how it describes what it
	// would do — vault.wrap.get's dry run looks a token up rather than
	// consuming it, on the same reasoning. Named here with the reason rather
	// than skipped, so the exemption is a sentence somebody can disagree with
	// and the default stays "touches nothing".
	previewReads := map[string]string{
		"s3.bucket.download": "lists the bucket to report how many objects and how many bytes, " +
			"and to refuse a key that would escape the destination before anything is written",
		"s3.bucket.upload": "lists the destination prefix to refuse landing on existing " +
			"objects before anything is sent",
	}
	for _, c := range Plugin().Capabilities {
		if c.Safety != plugin.Write && c.Safety != plugin.Destructive {
			continue
		}
		t.Run(c.ID, func(t *testing.T) {
			srv, asked := recordingS3(t, emptyListingXML)
			dir := t.TempDir()
			values := map[string]any{"bucket": "test-bucket", "key": "some/key"}
			switch c.ID {
			case "s3.object.copy", "s3.object.rename":
				values["dest-key"] = "some/other"
			case "s3.object.set":
				values["value"] = "content"
			case "s3.object.get":
				values["out"] = filepath.Join(dir, "got.bin")
			case "s3.bucket.download":
				values["out"] = filepath.Join(dir, "copy")
			case "s3.bucket.upload":
				// A separate TempDir, not dir: the walk runs before the
				// dry-run branch, so the source must exist and hold a file —
				// and pre-creating it in dir would read as the stray write
				// this test hunts.
				values["dir"] = dirWithFiles(t, map[string]string{"a.txt": "x"})
			}

			v, err := c.Run(t.Context(), dryReq(t, c.ID, endpointOf(t, srv), values))
			if err != nil {
				t.Fatalf("dry run failed: %v", err)
			}
			if v == nil {
				t.Fatal("dry run returned nothing — a preview that says nothing is not a preview")
			}
			if hits := asked(); len(hits) > 0 {
				why, allowed := previewReads[c.ID]
				switch {
				case !allowed:
					t.Errorf("--dry-run reached the server: %v", hits)
				default:
					// Even an allowed preview reads. Anything that is not a
					// GET is the act itself wearing a preview's clothes.
					for _, h := range hits {
						if !strings.HasPrefix(h, "GET ") {
							t.Errorf("--dry-run (%s) sent %s, which is not a read", why, h)
						}
					}
				}
			}
			if entries, _ := os.ReadDir(dir); len(entries) > 0 {
				var names []string
				for _, e := range entries {
					names = append(names, e.Name())
				}
				t.Errorf("--dry-run wrote %v", names)
			}
		})
	}
}

// A signature is spent the moment it exists. A presigned URL is a bearer
// credential nobody checks again for its whole ttl, so printing one under
// --dry-run gives away exactly what the real call gives away — the preview
// has to report the terms and withhold the signature.
func TestPresignDryRunReturnsNoSignature(t *testing.T) {
	srv, _ := recordingS3(t, "")

	v, err := runObjectPresign(t.Context(), dryReq(t, "s3.object.presign", endpointOf(t, srv),
		map[string]any{"bucket": "test-bucket", "key": "some/key"}))
	if err != nil {
		t.Fatal(err)
	}
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("got %T, want view.KeyValue", v)
	}
	for _, p := range kv.Pairs {
		if strings.Contains(p.Value, "X-Amz-Signature") || strings.Contains(p.Value, "Signature=") {
			t.Errorf("--dry-run handed out a working URL: %s = %s", p.Key, p.Value)
		}
	}
}

// --out never overwrites, and a download that fails partway leaves nothing
// behind. The old code opened the destination O_TRUNC before the first byte
// arrived, so pointing a failed download — or a --dry-run, which never
// reached the copy at all — at a file you cared about emptied it.
func TestObjectGetNeverEatsAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "important.tar")
	if err := os.WriteFile(out, []byte("the only copy"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A server that accepts the request and then dies mid-body: the failure
	// mode that used to leave a truncated file under a name saying otherwise.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	_, err := runObjectGet(t.Context(), reqFor(t, "s3.object.get", endpointOf(t, srv),
		map[string]any{"bucket": "test-bucket", "key": "some/key", "out": out}))
	if err == nil {
		t.Fatal("a truncated body was reported as a complete download")
	}
	got, rerr := os.ReadFile(out)
	if rerr != nil {
		t.Fatalf("the destination is gone: %v", rerr)
	}
	if string(got) != "the only copy" {
		t.Errorf("the destination was rewritten: %q", got)
	}
}

// The other half, and the one a probe found uncovered: when the destination
// did *not* exist, O_EXCL lets the write begin, so a body that dies partway
// leaves a file. It has to be removed — a truncated object under a name that
// says it is whole is the artifact this family refuses to produce, and the
// operator is better off where they started than holding a plausible-looking
// prefix.
//
// TestObjectGetNeverEatsAnExistingDestination cannot see this: O_EXCL refuses
// before anything is written there, so that test exercises the refusal and
// never the cleanup.
func TestObjectGetRemovesWhatAFailedDownloadWrote(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "fresh.tar")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1024")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler)
	}))
	defer srv.Close()

	_, err := runObjectGet(t.Context(), reqFor(t, "s3.object.get", endpointOf(t, srv),
		map[string]any{"bucket": "test-bucket", "key": "some/key", "out": out}))
	if err == nil {
		t.Fatal("a truncated body was reported as a complete download")
	}
	if _, serr := os.Stat(out); serr == nil {
		body, _ := os.ReadFile(out)
		t.Errorf("a failed download left %s behind holding %q", out, body)
	}
}
