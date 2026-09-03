package main

import (
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

// newSnapshotServer routes by "METHOD /path" and records every call in
// order. fakeQdrant cannot drive these handlers: a snapshot download is
// binary, an upload is multipart, and both sides of the transfer care about
// the method, which a body-per-path map cannot express.
func newSnapshotServer(t *testing.T,
	routes map[string]http.HandlerFunc) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		calls = append(calls, key)
		if h, ok := routes[key]; ok {
			h(w, r)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":{"error":"Not found"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// envelope wraps a result the way Qdrant's REST API does.
func envelope(result string) string {
	return `{"result":` + result + `,"status":"ok","time":0}`
}

func dryReq(t *testing.T, capID string, values map[string]any) plugin.Request {
	t.Helper()
	for _, c := range Plugin().Capabilities {
		if c.ID == capID {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), true, false)
		}
	}
	t.Fatalf("no capability %q", capID)
	return plugin.Request{}
}

func pairValue(t *testing.T, v view.View, key string) string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("view is %T, want KeyValue", v)
	}
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	t.Fatalf("no %q pair in %v", key, kv.Pairs)
	return ""
}

// An agent must never be able to pull a whole collection — payloads and
// vectors both — through the surface grants exist to gate. The refusal is
// marked as a refusal, so the ledger files it under policy rather than
// under "the work broke".
func TestDumpRefusesMCP(t *testing.T) {
	r := req(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": filepath.Join(t.TempDir(), "d.snapshot"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runDump(t.Context(), r)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "qdrant.human" {
		t.Fatalf("err = %v, want qdrant.human", err)
	}
	if !verr.Refusal {
		t.Error("the MCP gate is not marked as a refusal — the ledger would file it as a failure")
	}
	if !strings.Contains(verr.Hint, "qdrant.points.scroll") {
		t.Errorf("the hint does not name the bounded alternative: %q", verr.Hint)
	}
}

func TestDumpRequiresAnOutput(t *testing.T) {
	_, err := runDump(t.Context(), req(t, "qdrant.dump", map[string]any{"collection": "docs"}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "qdrant.dump.nooutput" {
		t.Fatalf("err = %v, want qdrant.dump.nooutput", err)
	}
}

// The early stat comes before any network call, so refusing an existing
// file costs the server nothing — and, more to the point, a dump that will
// be refused never makes the server snapshot a collection first.
func TestDumpRefusesAnExistingFileBeforeTouchingTheServer(t *testing.T) {
	srv, calls := newSnapshotServer(t, nil)
	path := filepath.Join(t.TempDir(), "d.snapshot")
	if err := os.WriteFile(path, []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := runDump(t.Context(), req(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": path,
		"endpoint": strings.TrimPrefix(srv.URL, "http://"),
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "qdrant.dump.exists" {
		t.Fatalf("err = %v, want qdrant.dump.exists", err)
	}
	if len(*calls) != 0 {
		t.Errorf("the server was reached %d times before the refusal, want 0: %v", len(*calls), *calls)
	}
}

func TestDumpDryRunTouchesNothing(t *testing.T) {
	srv, calls := newSnapshotServer(t, nil)
	path := filepath.Join(t.TempDir(), "d.snapshot")
	v, err := runDump(t.Context(), dryReq(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": path,
		"endpoint": strings.TrimPrefix(srv.URL, "http://"),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Errorf("dry run reached the server %d times, want 0: %v", len(*calls), *calls)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("dry run created the output file")
	}
	if _, ok := v.(view.Text); !ok {
		t.Errorf("dry run answered %T, want a Text description", v)
	}
}

func TestDumpCreatesDownloadsAndDeletes(t *testing.T) {
	snapshot := []byte("binary-snapshot-bytes\x00\x01\x02")
	srv, calls := newSnapshotServer(t, map[string]http.HandlerFunc{
		"GET /collections/docs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`{"points_count": 42, "segments_count": 1, "status": "green"}`)))
		},
		"GET /": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"title":"qdrant","version":"1.12.0"}`))
		},
		"POST /collections/docs/snapshots": func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("wait") != "true" {
				t.Error("snapshot creation was not asked to wait")
			}
			_, _ = w.Write([]byte(envelope(`{"name":"docs-2026.snapshot","size":26}`)))
		},
		"GET /collections/docs/snapshots/docs-2026.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(snapshot)
		},
		"DELETE /collections/docs/snapshots/docs-2026.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`true`)))
		},
	})

	path := filepath.Join(t.TempDir(), "docs.snapshot")
	v, err := runDump(t.Context(), req(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": path,
		"endpoint": strings.TrimPrefix(srv.URL, "http://"),
	}))
	if err != nil {
		t.Fatal(err)
	}

	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != string(snapshot) {
		t.Errorf("file holds %q, want the snapshot bytes", got)
	}
	info, _ := os.Stat(path)
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("file mode = %o, want 0600 — this is every payload and vector in the collection", perm)
	}

	// Create before download before delete, with the source described first:
	// the order is the design, not an accident of the implementation.
	want := []string{
		"GET /collections/docs",
		"GET /",
		"POST /collections/docs/snapshots",
		"GET /collections/docs/snapshots/docs-2026.snapshot",
		"DELETE /collections/docs/snapshots/docs-2026.snapshot",
	}
	if fmt.Sprint(*calls) != fmt.Sprint(want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}

	if restore := pairValue(t, v, "restore with"); !strings.Contains(restore, "rta qdrant restore docs") {
		t.Errorf("the receipt does not name the restore command: %q", restore)
	}
	if at := pairValue(t, v, "at rest"); !strings.Contains(at, "unencrypted") {
		t.Errorf("the receipt does not say the file is unencrypted: %q", at)
	}
	if c := pairValue(t, v, "contents"); !strings.Contains(c, "42 points") {
		t.Errorf("the receipt does not report what was dumped: %q", c)
	}
}

// A failed transfer must remove its half-written file — a partial snapshot
// is the one that gets restored six months later — and must still delete the
// server-side copy, or every broken download also eats the server's disk.
func TestDumpFailedDownloadCleansUpBothSides(t *testing.T) {
	srv, calls := newSnapshotServer(t, map[string]http.HandlerFunc{
		"GET /collections/docs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`{"points_count": 1, "segments_count": 1}`)))
		},
		"GET /": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"1.12.0"}`))
		},
		"POST /collections/docs/snapshots": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`{"name":"s.snapshot","size":1}`)))
		},
		"GET /collections/docs/snapshots/s.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"status":{"error":"storage failure"}}`))
		},
		"DELETE /collections/docs/snapshots/s.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`true`)))
		},
	})

	path := filepath.Join(t.TempDir(), "docs.snapshot")
	_, err := runDump(t.Context(), req(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": path,
		"endpoint": strings.TrimPrefix(srv.URL, "http://"),
	}))
	if err == nil {
		t.Fatal("a failed download reported success")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the partial file survived the failure")
	}
	deleted := false
	for _, c := range *calls {
		if strings.HasPrefix(c, "DELETE ") {
			deleted = true
		}
	}
	if !deleted {
		t.Errorf("the server-side snapshot was not deleted after the failed download: %v", *calls)
	}
}

// The leftover is reported, not fatal: the dump is safely local, and
// failing it retroactively would tell the operator their backup does not
// exist when it does.
func TestDumpReportsAnUndeletableServerCopy(t *testing.T) {
	srv, _ := newSnapshotServer(t, map[string]http.HandlerFunc{
		"GET /collections/docs": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`{"points_count": 1, "segments_count": 1}`)))
		},
		"GET /": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"1.12.0"}`))
		},
		"POST /collections/docs/snapshots": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(envelope(`{"name":"s.snapshot","size":1}`)))
		},
		"GET /collections/docs/snapshots/s.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("bytes"))
		},
		"DELETE /collections/docs/snapshots/s.snapshot": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":{"error":"busy"}}`))
		},
	})

	path := filepath.Join(t.TempDir(), "docs.snapshot")
	v, err := runDump(t.Context(), req(t, "qdrant.dump", map[string]any{
		"collection": "docs", "out": path,
		"endpoint": strings.TrimPrefix(srv.URL, "http://"),
	}))
	if err != nil {
		t.Fatalf("an undeletable server copy failed the dump: %v", err)
	}
	if leftover := pairValue(t, v, "leftover"); !strings.Contains(leftover, "s.snapshot") {
		t.Errorf("the receipt does not name the leftover snapshot: %q", leftover)
	}
}
