package kv

import (
	"strings"
	"testing"
)

// Reveal is the one place that bypasses kv.get's grant gate, on the strength
// of three documented invariants. Only internal/profile's own tests exercise
// it, and only through a stub matching its shape — nothing in this package,
// where the real function and a real encrypted store both live, ever calls
// it for real. These tests do.

func TestRevealReturnsTheStoredValueAsAPlainString(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)

	v, verr := Reveal("db-password")
	if verr != nil {
		t.Fatalf("Reveal: %v", verr)
	}
	if v != "s3cr3t" {
		t.Errorf("Reveal = %q, want %q", v, "s3cr3t")
	}
}

// The entry names in an operator's store are not something a profile's own
// resolution failure should hand to whatever is asking — an agent holding
// only a grant on the profile has no business learning what else is there.
func TestRevealDoesNotEnumerateOnNotFound(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	text(t, runSet, map[string]any{"key": "a-real-entry", "value": "x"}, false)

	_, verr := Reveal("does-not-exist")
	if verr == nil {
		t.Fatal("Reveal found a key that was never stored")
	}
	if strings.Contains(verr.Message, "a-real-entry") || strings.Contains(verr.Hint, "a-real-entry") {
		t.Fatalf("the not-found error names an entry that does exist: %+v", verr)
	}
}

// The request Reveal builds has no surface, so canPrompt is false by
// construction — but this asserts the observable consequence rather than
// the mechanism: with no passphrase available anywhere, Reveal must fail
// fast, and must never be the thing that calls the (blocking, real
// terminal) prompt.
func TestRevealNeverPrompts(t *testing.T) {
	setup(t)                                                         // passphraseEnv and identityEnv both cleared
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false) // creates the store under req()'s own default passphrase

	old := promptPassphrase
	promptPassphrase = func() (string, error) {
		t.Fatal("Reveal must never prompt for a passphrase")
		return "", nil
	}
	t.Cleanup(func() { promptPassphrase = old })

	if _, verr := Reveal("k"); verr == nil {
		t.Fatal("Reveal opened a store it has no passphrase for")
	}
}

func TestRevealOfAnEmptyKeyIsRefused(t *testing.T) {
	setup(t)
	if _, verr := Reveal(""); verr == nil {
		t.Fatal("Reveal accepted an empty key")
	}
}

func TestNamesListsStoredEntriesSortedAndNothingElse(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	for _, k := range []string{"zebra", "apple", "mango"} {
		text(t, runSet, map[string]any{"key": k, "value": "x"}, false)
	}
	got := Names()
	want := []string{"apple", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("Names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Names = %v, want %v", got, want)
		}
	}
}

// A store Reveal cannot open (wrong or missing passphrase) is a picker with
// nothing to offer, not an error — the caller is drawing a screen, not
// authenticating.
func TestNamesOnAnUnopenableStoreYieldsNothing(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	t.Setenv(passphraseEnv, "the-wrong-one")
	if got := Names(); len(got) != 0 {
		t.Fatalf("Names = %v, want none from a store that cannot be opened", got)
	}
}

func TestStoreWritesAnEntryRevealCanThenRead(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	text(t, runSet, map[string]any{"key": "seed", "value": "x"}, false) // creates the store

	if verr := Store("new-cred", "hunter2", "from the TUI", "profile:staging"); verr != nil {
		t.Fatalf("Store: %v", verr)
	}
	v, verr := Reveal("new-cred")
	if verr != nil || v != "hunter2" {
		t.Fatalf("Reveal after Store = %q, %v", v, verr)
	}
}

func TestStoreRefusesToOverwrite(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	text(t, runSet, map[string]any{"key": "already-here", "value": "original"}, false)

	if verr := Store("already-here", "replacement", "", ""); verr == nil {
		t.Fatal("Store silently replaced an existing entry")
	}
	v, verr := Reveal("already-here")
	if verr != nil || v != "original" {
		t.Fatalf("the original entry was changed: %q %v", v, verr)
	}
}

func TestStoreStampChangesWhenTheStoreDoes(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	before := StoreStamp()
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)
	after := StoreStamp()
	if before == after {
		t.Fatal("StoreStamp did not change after a write")
	}
	if same := StoreStamp(); same != after {
		t.Fatalf("StoreStamp is not stable across two reads of the same file: %q != %q", same, after)
	}
}

func TestStoreStampOfAMissingStoreIsEmpty(t *testing.T) {
	setup(t)
	if got := StoreStamp(); got != "" {
		t.Errorf("StoreStamp = %q, want empty for a store that does not exist", got)
	}
}
