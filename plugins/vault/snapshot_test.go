package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// snapshotArchive builds what Vault actually sends: a gzipped tar. The
// client verifies the stream as it copies, looking for a non-empty
// SHA256SUMS.sealed — the entry Vault writes last and encrypts with the seal
// — so a fixture of arbitrary bytes is rejected, and rightly.
//
// sealed=false is the failure that matters: an archive that is well-formed
// and stops short of its checksums, which is what a seal becoming
// unavailable midstream produces.
func snapshotArchive(t *testing.T, sealed bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	write := func(name, body string) {
		t.Helper()
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("meta.json", `{"ID":"x","Index":1,"Term":1}`)
	write("state.bin", "raft-state")
	write("SHA256SUMS", "abc  state.bin\n")
	if sealed {
		write("SHA256SUMS.sealed", "vault:v1:ciphertext")
	}

	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// snapshotServer answers the raft snapshot endpoint with body, and anything
// else with 404 — which is also the real shape of a Vault that is not on
// integrated storage.
func snapshotServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/sys/storage/raft/snapshot") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func snapshotReq(t *testing.T, srv *httptest.Server, out string) plugin.Request {
	t.Helper()
	return req(t, "vault.snapshot", map[string]any{
		"address": srv.URL, "token": "t", "out": out,
	})
}

func TestASnapshotIsWrittenSealedAndAtTheRightMode(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, true))
	path := filepath.Join(t.TempDir(), "vault.snap")

	v, err := runSnapshot(context.Background(), snapshotReq(t, srv, path))
	if err != nil {
		t.Fatalf("snapshot failed: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil || len(body) == 0 {
		t.Fatalf("file = %d bytes, %v", len(body), err)
	}
	// What landed is the archive Vault sent, byte for byte.
	if !bytes.Equal(body, snapshotArchive(t, true)) {
		t.Error("the file is not the archive the server sent")
	}
	// Every byte of a Vault's storage: 0600, set at creation.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("want KeyValue, got %s", view.TypeOf(v))
	}
	pairs := map[string]string{}
	for _, p := range kv.Pairs {
		pairs[p.Key] = p.Value
	}
	// **The property that makes this artifact different from a KV export**,
	// said where somebody is looking at the file they just made.
	if !strings.Contains(pairs["at rest"], "sealed") {
		t.Errorf("at rest = %q, want it to say the snapshot is still sealed", pairs["at rest"])
	}
	// A backup capability that does not say how to restore is the shape of
	// every backup that turned out not to be one.
	if !strings.Contains(pairs["restore with"], "raft snapshot restore") {
		t.Errorf("restore with = %q", pairs["restore with"])
	}
}

// The whole-Vault snapshot has no blast radius a grant could name, so it
// leaves the agent surface rather than asking for one.
func TestTheSnapshotRefusesMCP(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, true))
	path := filepath.Join(t.TempDir(), "vault.snap")

	_, err := runSnapshot(context.Background(),
		snapshotReq(t, srv, path).WithSurface(plugin.SurfaceMCP))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "vault.human" || !verr.Refusal {
		t.Fatalf("err = %v, want vault.human marked a refusal", err)
	}
	if !strings.Contains(verr.Hint, "vault.kv.get") {
		t.Errorf("hint = %q, want it to name the capability that does take a grant", verr.Hint)
	}
	// And it refused before writing anything.
	if _, err := os.Stat(path); err == nil {
		t.Error("the refused call still wrote a file")
	}
}

// A snapshot is never written over an existing file, the discipline
// keys.restore applies to a restored key and pg.dump to a database dump.
func TestAnExistingSnapshotIsNeverOverwritten(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, true))
	path := filepath.Join(t.TempDir(), "vault.snap")
	if err := os.WriteFile(path, []byte("the snapshot that already existed"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := runSnapshot(context.Background(), snapshotReq(t, srv, path))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "vault.snapshot.exists" {
		t.Fatalf("err = %v, want vault.snapshot.exists", err)
	}
	if body, _ := os.ReadFile(path); string(body) != "the snapshot that already existed" {
		t.Errorf("the existing file was disturbed: %q", body)
	}
}

// **A Vault on any storage but raft has no such endpoint**, and "404" against
// a URL nobody typed sends somebody looking for a path problem.
func TestAVaultWithoutRaftStorageSaysSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[]}`))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "vault.snap")

	_, err := runSnapshot(context.Background(), snapshotReq(t, srv, path))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "vault.snapshot.unsupported" {
		t.Fatalf("err = %v, want vault.snapshot.unsupported", err)
	}
	// The refusal states what rta will not do instead, because reaching for a
	// KV export here is exactly the mistake this capability exists to avoid.
	if !strings.Contains(verr.Hint, "KV export") {
		t.Errorf("hint = %q, want it to rule out the export substitute", verr.Hint)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a failed snapshot left a file behind")
	}
}

// **The failure that produces a file rather than an error.** Vault writes
// SHA256SUMS.sealed last and encrypts it with the seal, so a seal that
// becomes unavailable partway through yields a well-formed archive missing
// exactly that entry. The client catches it — and catches it *after* copying
// the bytes, so the file exists by then. Removing it is the whole protection:
// an unrestorable archive that looks like a backup is the worst artifact this
// capability could leave behind.
func TestASnapshotThatStopsShortOfItsChecksumsIsNotLeftOnDisk(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, false))
	path := filepath.Join(t.TempDir(), "vault.snap")

	_, err := runSnapshot(context.Background(), snapshotReq(t, srv, path))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "vault.snapshot.incomplete" {
		t.Fatalf("err = %v, want vault.snapshot.incomplete", err)
	}
	if !strings.Contains(verr.Hint, "seal") {
		t.Errorf("hint = %q, want it to name the seal", verr.Hint)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("an unrestorable archive was left on disk, where it reads as a backup")
	}
}

// --dry-run writes nothing, the host's promise for any write capability and
// one a capability that creates a file has to keep explicitly.
func TestSnapshotDryRunCreatesNothing(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, true))
	path := filepath.Join(t.TempDir(), "vault.snap")

	c := capabilityByID(t, "vault.snapshot")
	dry := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: map[string]any{
		"address": srv.URL, "token": "t", "out": path,
	}}), true, false)

	if _, err := runSnapshot(context.Background(), dry); err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("--dry-run created the file")
	}
}

// Naming no destination is refused with the flag named, rather than reported
// as something missing.
func TestSnapshotWithNoDestinationNamesTheFlag(t *testing.T) {
	srv := snapshotServer(t, snapshotArchive(t, true))
	_, err := runSnapshot(context.Background(), snapshotReq(t, srv, ""))
	var verr *view.Error
	if !errors.As(err, &verr) || verr.Code != "vault.snapshot.nooutput" {
		t.Fatalf("err = %v, want vault.snapshot.nooutput", err)
	}
	if !strings.Contains(verr.Hint, "--out") {
		t.Errorf("hint = %q, want it to name the flag", verr.Hint)
	}
}

// The destination is Local, so an MCP caller can never choose which file on
// this host gets written. Both that and the outright MCP refusal hold,
// because they protect against different mistakes.
func TestTheSnapshotDestinationIsNeverCallerChosen(t *testing.T) {
	c := capabilityByID(t, "vault.snapshot")
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
