package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// restoreServer answers the raft restore endpoints, capturing what was
// posted to which path, and answers seal-status without a token — the same
// asymmetry the real Vault has, and the one runRestoreSnapshot's read-back
// depends on.
func restoreServer(t *testing.T, status int, errBody string) (*httptest.Server, *[]string, *string) {
	t.Helper()
	var paths []string
	var uploaded string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/sys/storage/raft/snapshot"):
			paths = append(paths, r.URL.Path)
			raw, _ := io.ReadAll(r.Body)
			uploaded = string(raw)
			if status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(errBody))
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/v1/sys/seal-status":
			paths = append(paths, r.URL.Path)
			_, _ = w.Write([]byte(`{"sealed":false,"cluster_name":"vault-cluster-1","version":"1.15.0"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"errors":[]}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths, &uploaded
}

func archiveOnDisk(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vault.snap")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func restorePair(t *testing.T, v view.View, key string) string {
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

func TestRestoreRefusesMCP(t *testing.T) {
	r := req(t, "vault.restore", map[string]any{
		"address": "http://127.0.0.1:1", "token": "t", "file": archiveOnDisk(t, "bytes"),
	}).WithSurface(plugin.SurfaceMCP)
	_, err := runRestoreSnapshot(t.Context(), r)
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.human" {
		t.Fatalf("err = %v, want vault.human", err)
	}
	if !verr.Refusal {
		t.Error("the MCP gate is not marked as a refusal — the ledger would file it as a failure")
	}
	if !strings.Contains(verr.Hint, "overwrites") {
		t.Errorf("the hint does not explain the arriving direction: %q", verr.Hint)
	}
}

func TestRestoreRefusesAMissingFile(t *testing.T) {
	_, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": "http://127.0.0.1:1", "token": "t",
		"file": filepath.Join(t.TempDir(), "nope.snap"),
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.restore.missing" {
		t.Fatalf("err = %v, want vault.restore.missing", err)
	}
}

func TestRestoreRefusesAnEmptyFile(t *testing.T) {
	_, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": "http://127.0.0.1:1", "token": "t", "file": archiveOnDisk(t, ""),
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.restore.empty" {
		t.Fatalf("err = %v, want vault.restore.empty", err)
	}
}

// The archive travels byte for byte, to the non-force endpoint unless
// --force says otherwise — and the receipt's read-back uses seal-status, the
// endpoint that still answers when the restore has replaced the very token
// that authorized it.
func TestRestoreStreamsTheArchiveAndReadsBackWithoutAToken(t *testing.T) {
	srv, paths, uploaded := restoreServer(t, 0, "")
	content := "gzip-tar-bytes\x1f\x8b"
	v, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": srv.URL, "token": "t", "file": archiveOnDisk(t, content),
	}))
	if err != nil {
		t.Fatal(err)
	}
	if *uploaded != content {
		t.Errorf("the server received %q, want the file's bytes", *uploaded)
	}
	for _, p := range *paths {
		if strings.HasSuffix(p, "snapshot-force") {
			t.Errorf("the non-force restore reached the force endpoint: %v", *paths)
		}
	}
	if g := restorePair(t, v, "guarantee"); !strings.Contains(g, "token that ran this restore") {
		t.Errorf("the receipt does not warn that the auth state was replaced: %q", g)
	}
	if after := restorePair(t, v, "afterwards"); !strings.Contains(after, "vault-cluster-1") {
		t.Errorf("the read-back is not the server's own answer: %q", after)
	}
}

func TestRestoreForceUsesTheForceEndpoint(t *testing.T) {
	srv, paths, _ := restoreServer(t, 0, "")
	_, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": srv.URL, "token": "t", "file": archiveOnDisk(t, "bytes"), "force": true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	forced := false
	for _, p := range *paths {
		if strings.HasSuffix(p, "snapshot-force") {
			forced = true
		}
	}
	if !forced {
		t.Errorf("--force did not use the force endpoint: %v", *paths)
	}
}

// The refusal that protects the unseal keys: a snapshot from another cluster
// is named as such, and the hint carries the flag and its price together —
// reaching for --force without the source cluster's keys bricks the Vault.
func TestRestoreMismatchNamesTheForceFlagAndItsPrice(t *testing.T) {
	srv, _, _ := restoreServer(t, http.StatusBadRequest,
		`{"errors":["could not verify hash file, possibly the snapshot is using a different set of unseal keys"]}`)
	_, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": srv.URL, "token": "t", "file": archiveOnDisk(t, "bytes"),
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.restore.mismatch" {
		t.Fatalf("err = %v, want vault.restore.mismatch", err)
	}
	if !strings.Contains(verr.Hint, "--force") || !strings.Contains(verr.Hint, "unseal") {
		t.Errorf("the hint does not carry the flag and its price together: %q", verr.Hint)
	}
}

func TestRestoreOnNonRaftStorageSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	t.Cleanup(srv.Close)
	_, err := runRestoreSnapshot(t.Context(), req(t, "vault.restore", map[string]any{
		"address": srv.URL, "token": "t", "file": archiveOnDisk(t, "bytes"),
	}))
	verr, ok := err.(*view.Error)
	if !ok || verr.Code != "vault.restore.unsupported" {
		t.Fatalf("err = %v, want vault.restore.unsupported", err)
	}
}
