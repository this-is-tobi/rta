package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// **An object key becomes a filename, and the key comes from the server.**
// Everything else in this plugin writes to a path the operator typed; this
// is the one capability that derives local paths from remote data, which
// makes the confinement below the thing to review rather than a detail.

// downloadServer lists the given keys and serves each one's body as its own
// name, so a test can tell which object landed where.
func downloadServer(t *testing.T, keys []string) *httptest.Server {
	t.Helper()
	type object struct {
		Key          string `xml:"Key"`
		LastModified string `xml:"LastModified"`
		Size         int64  `xml:"Size"`
		ETag         string `xml:"ETag"`
	}
	type result struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		Name     string   `xml:"Name"`
		Contents []object `xml:"Contents"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("list-type") || r.URL.Path == "/test-bucket/" {
			res := result{Name: "test-bucket"}
			for _, k := range keys {
				res.Contents = append(res.Contents, object{
					Key: k, Size: int64(len("body:" + k)), ETag: `"e"`,
					LastModified: "2026-08-01T10:00:00.000Z",
				})
			}
			w.Header().Set("Content-Type", "application/xml")
			_ = xml.NewEncoder(w).Encode(res)
			return
		}
		// A GET for one object: the body says which key it was. The
		// Last-Modified header is required — minio-go parses it and fails the
		// whole request without one.
		key := strings.TrimPrefix(r.URL.Path, "/test-bucket/")
		w.Header().Set("Last-Modified", "Mon, 01 Sep 2026 10:00:00 GMT")
		_, _ = w.Write([]byte("body:" + key))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func downloadReq(t *testing.T, srv *httptest.Server, values map[string]any) plugin.Request {
	t.Helper()
	values["bucket"] = "test-bucket"
	return reqFor(t, "s3.bucket.download", endpointOf(t, srv), values)
}

// **The traversal table.** Each of these is a legal S3 key, and each one
// would land outside the destination if the key were simply joined onto it.
// Asserted on destinationFor directly, so the rule is tested rather than one
// path through a download.
func TestAKeyCanNeverEscapeTheDestination(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{
		"../escaped",
		"../../etc/passwd",
		"a/../../escaped",
		"a/b/../../../escaped",
		"/../escaped",
		"//../../escaped",
		"./../escaped",
		"a/./../../escaped",
	} {
		got, err := destinationFor(root, key)
		if err == nil {
			t.Errorf("key %q resolved to %q instead of being refused", key, got)
		}
	}
}

// And the ordinary keys still work, or the check above would be satisfied by
// refusing everything.
func TestOrdinaryKeysResolveUnderTheDestination(t *testing.T) {
	root := t.TempDir()
	for key, want := range map[string]string{
		"file.txt":           "file.txt",
		"a/b/c.txt":          filepath.Join("a", "b", "c.txt"),
		"/leading-slash.txt": "leading-slash.txt",
		"a/../b.txt":         "b.txt",
		"dots../weird..name": filepath.Join("dots..", "weird..name"),
		"..hidden":           "..hidden",
		"a/..b/c":            filepath.Join("a", "..b", "c"),
	} {
		got, err := destinationFor(root, key)
		if err != nil {
			t.Errorf("key %q was refused: %v", key, err)
			continue
		}
		if got != filepath.Join(root, want) {
			t.Errorf("key %q resolved to %q, want %q", key, got, filepath.Join(root, want))
		}
	}
}

// Keys that name nothing to write.
func TestDegenerateKeysAreRefused(t *testing.T) {
	root := t.TempDir()
	for _, key := range []string{"", "/", "///", ".", "a/..", "with\x00nul"} {
		if got, err := destinationFor(root, key); err == nil {
			t.Errorf("key %q resolved to %q instead of being refused", key, got)
		}
	}
}

// **Refused, not skipped, and before anything is written.** A key that
// cannot be stored under the destination is either an attack or a bucket
// nobody understands, and both deserve a person looking rather than a line
// in a summary — a directory that looks like a complete backup and is not is
// the artifact this family of capabilities exists to avoid.
func TestOneHostileKeyRefusesTheWholeDownloadAndWritesNothing(t *testing.T) {
	srv := downloadServer(t, []string{"good/a.txt", "../../../etc/cron.d/root", "good/b.txt"})
	root := filepath.Join(t.TempDir(), "backup")

	_, err := runBucketDownload(context.Background(), downloadReq(t, srv, map[string]any{"out": root}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.download.unsafekey" {
		t.Fatalf("err = %v, want s3.download.unsafekey", err)
	}
	if !strings.Contains(verr.Message, "cron.d") {
		t.Errorf("message = %q, want it to name the offending key", verr.Message)
	}
	// Nothing written at all — not the destination, and certainly not the
	// two keys that were fine.
	if _, statErr := os.Stat(root); statErr == nil {
		t.Error("the refused download still created its directory")
	}
}

func TestABucketIsCopiedWithItsLayoutAndModes(t *testing.T) {
	srv := downloadServer(t, []string{"a.txt", "nested/b.txt", "nested/deep/c.txt"})
	root := filepath.Join(t.TempDir(), "backup")

	v, err := runBucketDownload(context.Background(),
		downloadReq(t, srv, map[string]any{"out": root, "parallel": 3}))
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}

	for key := range map[string]bool{"a.txt": true, "nested/b.txt": true, "nested/deep/c.txt": true} {
		path := filepath.Join(root, filepath.FromSlash(key))
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Errorf("%s: %v", key, readErr)
			continue
		}
		if string(body) != "body:"+key {
			t.Errorf("%s holds %q", key, body)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		// Every object in a bucket, on somebody's laptop.
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("%s mode = %o, want 600", key, perm)
		}
	}
	if info, statErr := os.Stat(root); statErr != nil {
		t.Fatal(statErr)
	} else if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("directory mode = %o, want 700", perm)
	}

	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	var objects string
	for _, p := range kv.Pairs {
		if p.Key == "objects" {
			objects = p.Value
		}
	}
	if objects != "3" {
		t.Errorf("objects = %q, want 3", objects)
	}
}

// A whole bucket has no blast radius a grant could name, so it leaves the
// agent surface rather than asking for one.
func TestTheDownloadRefusesMCP(t *testing.T) {
	srv := downloadServer(t, []string{"a.txt"})
	root := filepath.Join(t.TempDir(), "backup")

	_, err := runBucketDownload(context.Background(),
		downloadReq(t, srv, map[string]any{"out": root}).WithSurface(plugin.SurfaceMCP))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.human" || !verr.Refusal {
		t.Fatalf("err = %v, want s3.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "s3.object.get") {
		t.Errorf("hint = %q, want it to name the capability that does take a grant", verr.Hint)
	}
	if _, statErr := os.Stat(root); statErr == nil {
		t.Error("the refused call still created a directory")
	}
}

// A download always makes its own directory, so what lands in it is one
// bucket at one moment rather than half of one run and half of another.
func TestAnExistingDirectoryIsNeverWrittenInto(t *testing.T) {
	srv := downloadServer(t, []string{"a.txt"})
	root := t.TempDir() // exists

	_, err := runBucketDownload(context.Background(), downloadReq(t, srv, map[string]any{"out": root}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.download.exists" {
		t.Fatalf("err = %v, want s3.download.exists", err)
	}
}

// Refused rather than truncated: a backup missing objects nobody named is
// worse than one that did not run.
func TestABucketOverTheLimitIsRefusedNotTruncated(t *testing.T) {
	var keys []string
	for i := range 20 {
		keys = append(keys, fmt.Sprintf("obj-%03d", i))
	}
	srv := downloadServer(t, keys)
	root := filepath.Join(t.TempDir(), "backup")

	_, err := runBucketDownload(context.Background(),
		downloadReq(t, srv, map[string]any{"out": root, "limit": 5}))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "s3.download.toomany" {
		t.Fatalf("err = %v, want s3.download.toomany", err)
	}
	if _, statErr := os.Stat(root); statErr == nil {
		t.Error("a refused download still created its directory")
	}
}

// A folder marker — the key the AWS console writes when somebody makes a
// "folder" — is a naming convention, not an object. `nested/` and
// `nested/a.txt` both resolve to a path named `nested`, so keeping the marker
// would mean writing a file where a directory has to go.
func TestFolderMarkersAreNotWrittenAsFiles(t *testing.T) {
	srv := downloadServer(t, []string{"nested/", "nested/a.txt"})
	root := filepath.Join(t.TempDir(), "backup")

	if _, err := runBucketDownload(context.Background(),
		downloadReq(t, srv, map[string]any{"out": root})); err != nil {
		t.Fatalf("download failed: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, "nested"))
	if err != nil {
		t.Fatalf("nested/: %v", err)
	}
	if !info.IsDir() {
		t.Error("the folder marker was written as a file where a directory has to go")
	}
	if body, err := os.ReadFile(filepath.Join(root, "nested", "a.txt")); err != nil {
		t.Errorf("the object under the marker was lost: %v", err)
	} else if string(body) != "body:nested/a.txt" {
		t.Errorf("nested/a.txt holds %q", body)
	}
}

// **A partial backup is the one that gets restored six months later**, so a
// download that fails part way takes the whole directory with it rather than
// leaving a plausible-looking subset.
//
// Found by probing: breaking the cleanup produced no failure, because
// nothing covered it.
func TestAFailedDownloadLeavesNoDirectoryBehind(t *testing.T) {
	keys := []string{"a.txt", "boom.txt", "c.txt"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("list-type") {
			var b strings.Builder
			b.WriteString(`<ListBucketResult><Name>test-bucket</Name>`)
			for _, k := range keys {
				fmt.Fprintf(&b, `<Contents><Key>%s</Key><Size>5</Size>`+
					`<LastModified>2026-08-01T10:00:00.000Z</LastModified>`+
					`<ETag>"e"</ETag></Contents>`, k)
			}
			b.WriteString(`</ListBucketResult>`)
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(b.String()))
			return
		}
		if strings.HasSuffix(r.URL.Path, "boom.txt") {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`<Error><Code>InternalError</Code></Error>`))
			return
		}
		w.Header().Set("Last-Modified", "Mon, 01 Sep 2026 10:00:00 GMT")
		_, _ = w.Write([]byte("body!"))
	}))
	defer srv.Close()
	root := filepath.Join(t.TempDir(), "backup")

	if _, err := runBucketDownload(context.Background(),
		downloadReq(t, srv, map[string]any{"out": root, "parallel": 1})); err == nil {
		t.Fatal("a download with a failing object reported success")
	}
	if _, err := os.Stat(root); err == nil {
		t.Error("a failed download left its partial directory, where it reads as a backup")
	}
}

// --dry-run writes nothing, the promise the host makes for any write
// capability. s3.object.get is this plugin's counter-example — it writes on
// --dry-run — so it is asserted here rather than assumed.
func TestDownloadDryRunCreatesNothing(t *testing.T) {
	srv := downloadServer(t, []string{"a.txt", "b.txt"})
	root := filepath.Join(t.TempDir(), "backup")

	c := capabilityByID(t, "s3.bucket.download")
	dry := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{
		"bucket": "test-bucket", "out": root,
		"endpoint": endpointOf(t, srv), "access-key": "test", "secret-key": "test",
	}}), true, false)

	v, err := runBucketDownload(context.Background(), dry)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if _, statErr := os.Stat(root); statErr == nil {
		t.Fatal("--dry-run created the directory")
	}
	text, ok := v.(view.Text)
	if !ok {
		t.Fatalf("want Text, got %s", view.TypeOf(v))
	}
	if !strings.Contains(text.Body, "2 objects") {
		t.Errorf("dry run = %q, want it to say how much it would copy", text.Body)
	}
}

// The destination is Local, so an MCP caller can never choose which
// directory on this host gets written. Both that and the outright MCP
// refusal hold, because they protect against different mistakes.
func TestTheDownloadDestinationIsNeverCallerChosen(t *testing.T) {
	c := capabilityByID(t, "s3.bucket.download")
	for _, f := range c.Inputs {
		if f.Type == plugin.Path && !f.Local {
			t.Errorf("path input %q is not Local", f.Name)
		}
	}
}

func capabilityByID(t *testing.T, id string) plugin.Capability {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no capability %q", id)
	return plugin.Capability{}
}
