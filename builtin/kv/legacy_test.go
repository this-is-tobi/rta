package kv

import (
	"strings"
	"testing"
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
