package kv

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The stamp sits in the same JSON document as the values, so a stamped store
// never has to be guessed at. An earlier version of decodeStore ignored it
// and inferred the format from whether the values parsed as base64 — while
// its own comment claimed nothing on disk could settle the question.
func TestDecodeStorePrefersTheVersionStampOverGuessing(t *testing.T) {
	// A current store whose every value happens to be valid base64 — the
	// exact shape the inference cannot tell from a legacy one.
	stamped := []byte(`{"version":2,"entries":{"a":{"value":"aGVsbG8=","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}}`)
	s, verr := decodeStore(stamped)
	if verr != nil {
		t.Fatalf("stamped store refused: %v", verr)
	}
	if got := string(s.Entries["a"].Value); got != "hello" {
		t.Errorf("stamped store decoded as %q, want the base64 decoded to %q", got, "hello")
	}

	// The same document without the stamp is the ambiguous case, and the
	// inference still applies to it — this pins which branch each takes.
	unstamped := []byte(`{"entries":{"a":{"value":"aGVsbG8=","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}}`)
	if _, verr := decodeStore(unstamped); verr != nil {
		t.Fatalf("unstamped store refused: %v", verr)
	}
}

// A legacy store still opens, which is the whole reason this file exists.
func TestDecodeStoreStillReadsAnUnstampedLegacyStore(t *testing.T) {
	legacy := []byte(`{"entries":{"db":{"value":"hunter2","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}}`)
	s, verr := decodeStore(legacy)
	if verr != nil {
		t.Fatalf("legacy store refused: %v", verr)
	}
	if got := string(s.Entries["db"].Value); got != "hunter2" {
		t.Errorf("legacy value = %q, want %q", got, "hunter2")
	}
}

// A stamped store that is genuinely broken must say so as itself, rather than
// falling through to the legacy parser and blaming the wrong format.
func TestDecodeStoreReportsAStampedStoreOnItsOwnTerms(t *testing.T) {
	_, verr := decodeStore([]byte(`{"version":2,"entries":{"a":{"value":123}}}`))
	if verr == nil {
		t.Fatal("a malformed stamped store was accepted")
	}
	if !strings.Contains(verr.Message, "version 2") {
		t.Errorf("the error should name the format it claims to be: %v", verr.Message)
	}
}

func TestNeedsBackupOnlyOnTheUpgradeWrite(t *testing.T) {
	if !needsBackup(0) {
		t.Error("an unstamped store must be backed up before it is replaced")
	}
	if needsBackup(storeVersion) {
		t.Error("a store already in the current format needs no backup")
	}
}

// A coverage test for a real gap review found:
// backupUnstamped's own doc comment calls the copy it makes "the one
// failure this package must not have" — a secrets store that quietly
// destroys a secret — and yet no test had ever placed a real, on-disk,
// unstamped store at storePath() and confirmed an upgrading write actually
// produces the backup, byte for byte. Every existing legacy test only
// decodes in-memory byte slices; every existing rekey/set test starts from
// a store that does not exist on disk yet, so backupUnstamped always took
// its early "no store yet" return.
func TestAnUpgradingWriteBacksUpTheOriginalCiphertextFirst(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "hunter2")

	// A real, on-disk, unstamped (pre-v2) store: current entry shape, but
	// with no "version" field, exactly what decodeStore treats as needing a
	// backup before the next write replaces it.
	original := []byte(`{"entries":{"k":{"value":"dg==","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}}`)
	r, err := age.NewScryptRecipient("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	r.SetWorkFactor(scryptWorkFactor)
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(original); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	ciphertext := buf.Bytes()
	if err := os.WriteFile(storePath(), ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "other", "value": "x"})); err != nil {
		t.Fatalf("upgrading write: %v", err)
	}

	backup := storePath() + ".pre-v2.bak"
	got, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("no backup was written: %v", err)
	}
	if !bytes.Equal(got, ciphertext) {
		t.Error("the backup does not match the original ciphertext byte for byte")
	}

	// And the upgrade itself must not have lost the pre-existing entry.
	v, err := runGet(context.Background(), cliReq(map[string]any{"key": "k"}))
	if err != nil {
		t.Fatalf("the pre-existing entry did not survive the upgrade: %v", err)
	}
	if v.(view.Text).Body != "v" {
		t.Errorf("got %v", v)
	}
}
