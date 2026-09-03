package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// uploadServer answers the pre-flight listing with the given XML and records
// every PUT's key and body, so a test can tell which file became which
// object — the mirror of downloadServer, watching the other direction.
func uploadServer(t *testing.T, listing string) (*httptest.Server, *map[string]string) {
	t.Helper()
	var mu sync.Mutex
	puts := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			// Over plain HTTP minio-go signs uploads with the streaming
			// signature, so the body arrives aws-chunked; the payload the
			// test compares is what a real S3 would store, not the framing.
			if strings.HasPrefix(r.Header.Get("X-Amz-Content-Sha256"), "STREAMING-") {
				body = decodeAWSChunks(t, body)
			}
			mu.Lock()
			puts[strings.TrimPrefix(r.URL.Path, "/test-bucket/")] = string(body)
			mu.Unlock()
			w.Header().Set("ETag", `"e"`)
		default:
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(listing))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &puts
}

// decodeAWSChunks unwraps the aws-chunked framing: repeated
// "<hex-size>;chunk-signature=…\r\n<data>\r\n" until a zero-size chunk.
func decodeAWSChunks(t *testing.T, body []byte) []byte {
	t.Helper()
	var out []byte
	rest := body
	for {
		head, tail, ok := strings.Cut(string(rest), "\r\n")
		if !ok {
			t.Fatalf("aws-chunked body with no header line: %q", rest)
		}
		sizeHex, _, _ := strings.Cut(head, ";")
		var size int64
		if _, err := fmt.Sscanf(sizeHex, "%x", &size); err != nil {
			t.Fatalf("aws-chunked size %q: %v", sizeHex, err)
		}
		if size == 0 {
			return out
		}
		if int64(len(tail)) < size {
			t.Fatalf("aws-chunked chunk shorter than its size %d: %q", size, tail)
		}
		out = append(out, tail[:size]...)
		rest = []byte(strings.TrimPrefix(tail[size:], "\r\n"))
	}
}

const emptyListing = `<ListBucketResult><Name>test-bucket</Name></ListBucketResult>`

const holdsOneObject = `<ListBucketResult><Name>test-bucket</Name><Contents>` +
	`<Key>backup/existing.txt</Key><Size>5</Size>` +
	`<LastModified>2026-08-01T10:00:00.000Z</LastModified><ETag>"e"</ETag>` +
	`</Contents></ListBucketResult>`

// dirWithFiles writes the named files (key → content) under a fresh
// directory and returns it.
func dirWithFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func uploadReq(t *testing.T, srv *httptest.Server, values map[string]any) plugin.Request {
	t.Helper()
	values["bucket"] = "test-bucket"
	return reqFor(t, "s3.bucket.upload", endpointOf(t, srv), values)
}

func TestTheUploadRefusesMCP(t *testing.T) {
	srv, puts := uploadServer(t, emptyListing)
	dir := dirWithFiles(t, map[string]string{"a.txt": "hello"})

	_, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": dir}).WithSurface(plugin.SurfaceMCP))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.human" || !verr.Refusal {
		t.Fatalf("err = %v, want s3.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "s3.object.set") {
		t.Errorf("hint = %q, want it to name the capability that does take a grant", verr.Hint)
	}
	if len(*puts) != 0 {
		t.Errorf("the refused call still uploaded: %v", *puts)
	}
}

func TestUploadRefusesAMissingDirectory(t *testing.T) {
	srv, _ := uploadServer(t, emptyListing)
	_, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": filepath.Join(t.TempDir(), "nope")}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.upload.missing" {
		t.Fatalf("err = %v, want s3.upload.missing", err)
	}
}

// An empty directory uploads nothing and would report success — the lie an
// empty dump file tells, in directory form.
func TestUploadRefusesAnEmptyDirectory(t *testing.T) {
	srv, _ := uploadServer(t, emptyListing)
	_, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": t.TempDir()}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.upload.empty" {
		t.Fatalf("err = %v, want s3.upload.empty", err)
	}
}

// **The traversal hazard, pointing the other way.** A symlink inside the
// directory would upload whatever it points at — a credential included — so
// it refuses the whole upload by name, before anything is sent.
func TestASymlinkRefusesTheWholeUploadAndSendsNothing(t *testing.T) {
	srv, puts := uploadServer(t, emptyListing)
	dir := dirWithFiles(t, map[string]string{"good.txt": "fine"})
	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "innocent.txt")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	_, err := runBucketUpload(context.Background(), uploadReq(t, srv, map[string]any{"dir": dir}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.upload.notregular" {
		t.Fatalf("err = %v, want s3.upload.notregular", err)
	}
	if !strings.Contains(verr.Message, "innocent.txt") {
		t.Errorf("message = %q, want it to name the symlink", verr.Message)
	}
	if len(*puts) != 0 {
		t.Errorf("the refused upload still sent objects: %v", *puts)
	}
}

// The layout survives the trip, and a prefix without a trailing slash still
// separates — the upload constructs keys, so "backup" + "a.txt" must become
// backup/a.txt, never backupa.txt.
func TestADirectoryIsUploadedWithItsLayoutUnderThePrefix(t *testing.T) {
	srv, puts := uploadServer(t, emptyListing)
	dir := dirWithFiles(t, map[string]string{
		"a.txt":        "body:a",
		"nested/b.txt": "body:b",
	})

	v, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": dir, "prefix": "backup", "parallel": 2}))
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}

	want := map[string]string{
		"backup/a.txt":        "body:a",
		"backup/nested/b.txt": "body:b",
	}
	for key, content := range want {
		if got := (*puts)[key]; got != content {
			t.Errorf("object %q holds %q, want %q (all puts: %v)", key, got, content, *puts)
		}
	}
	if len(*puts) != len(want) {
		t.Errorf("puts = %v, want exactly %d objects", *puts, len(want))
	}

	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	for _, p := range kv.Pairs {
		if p.Key == "objects" && p.Value != "2" {
			t.Errorf("objects = %q, want 2", p.Value)
		}
	}
}

// The download's fresh-directory rule pointing the other way: a destination
// already holding objects under the prefix is somebody's data, and it is
// never silently landed on.
func TestUploadRefusesANonEmptyDestination(t *testing.T) {
	srv, puts := uploadServer(t, holdsOneObject)
	dir := dirWithFiles(t, map[string]string{"a.txt": "new"})

	_, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": dir, "prefix": "backup"}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.upload.notempty" {
		t.Fatalf("err = %v, want s3.upload.notempty", err)
	}
	if !strings.Contains(verr.Message, "backup/existing.txt") {
		t.Errorf("message = %q, want it to name what is already there", verr.Message)
	}
	if len(*puts) != 0 {
		t.Errorf("the refused upload still sent objects: %v", *puts)
	}
}

func TestOverwriteProceedsOntoANonEmptyDestinationAndSaysSo(t *testing.T) {
	srv, puts := uploadServer(t, holdsOneObject)
	dir := dirWithFiles(t, map[string]string{"a.txt": "new"})

	v, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": dir, "prefix": "backup", "overwrite": true}))
	if err != nil {
		t.Fatalf("upload with --overwrite failed: %v", err)
	}
	if (*puts)["backup/a.txt"] != "new" {
		t.Errorf("puts = %v, want backup/a.txt uploaded", *puts)
	}
	kv := v.(view.KeyValue)
	found := false
	for _, p := range kv.Pairs {
		if p.Key == "overwrite" {
			found = true
		}
	}
	if !found {
		t.Error("the receipt does not say --overwrite was in effect")
	}
}

// Refused rather than truncated, the family rule: a restore missing files
// nobody named is worse than one that did not run.
func TestUploadOverTheLimitIsRefusedNotTruncated(t *testing.T) {
	srv, puts := uploadServer(t, emptyListing)
	files := map[string]string{}
	for i := range 5 {
		files[fmt.Sprintf("f-%d.txt", i)] = "x"
	}
	dir := dirWithFiles(t, files)

	_, err := runBucketUpload(context.Background(),
		uploadReq(t, srv, map[string]any{"dir": dir, "limit": 3}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.upload.toomany" {
		t.Fatalf("err = %v, want s3.upload.toomany", err)
	}
	if len(*puts) != 0 {
		t.Errorf("the refused upload still sent objects: %v", *puts)
	}
}

// --dry-run may list — the destination check is how it refuses landing on
// existing objects before anything is sent — and must send nothing.
func TestUploadDryRunListsButSendsNothing(t *testing.T) {
	srv, puts := uploadServer(t, emptyListing)
	dir := dirWithFiles(t, map[string]string{"a.txt": "hello", "b.txt": "world"})

	v, err := runBucketUpload(context.Background(),
		dryReq(t, "s3.bucket.upload", endpointOf(t, srv), map[string]any{
			"bucket": "test-bucket", "dir": dir,
		}))
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if len(*puts) != 0 {
		t.Errorf("--dry-run uploaded: %v", *puts)
	}
	text, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(text.Body, "2 files") {
		t.Errorf("dry run = %q, want it to say how much it would send", text.Body)
	}
}

// The source is Local, so an MCP caller can never choose which directory on
// this host gets read. Both that and the outright MCP refusal hold, because
// they protect against different mistakes.
func TestTheUploadSourceIsNeverCallerChosen(t *testing.T) {
	c := capabilityByID(t, "s3.bucket.upload")
	for _, f := range c.Inputs {
		if f.Type == plugin.Path && !f.Local {
			t.Errorf("path input %q is not Local", f.Name)
		}
	}
}
