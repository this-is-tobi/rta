package kv

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
	// Reveal cannot tell who reads what it refuses with, so the command is
	// handed on as the operator's, which is safe to read from either side.
	if !strings.HasPrefix(verr.Hint, plugin.AskOperator("kv list")) {
		t.Errorf("the hint hands on a command without saying whose it is: %q", verr.Hint)
	}
}

// The request Reveal builds has no surface, so canPrompt is false by
// construction — but this asserts the observable consequence rather than
// the mechanism: with no passphrase available anywhere, Reveal must fail
// fast, and must never be the thing that calls the (blocking, real
// terminal) prompt.
// A profile can map a credential onto an entry on a machine with no store at
// all, and "no entry in the store" then sends a person to `kv list`, which
// shows an empty table. The refusal says what is missing, and the command that
// makes it.
func TestRevealWithNoStoreSaysThereIsNoneAndHowOneIsMade(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	_, verr := Reveal("staging-db-password")
	if verr == nil {
		t.Fatal("Reveal found a key in a store that does not exist")
	}
	if !strings.Contains(verr.Message, "no store on this machine yet") {
		t.Errorf("the refusal does not say there is no store: %q", verr.Message)
	}
	if !strings.HasPrefix(verr.Hint, plugin.AskOperator("kv set staging-db-password")) {
		t.Errorf("the hint does not hand on the command that makes the store, as the operator's: %q", verr.Hint)
	}
}

// A person at a terminal is the operator and has nobody to ask, so what their
// own profile refuses with is the command to run, spelled for where they are —
// "ask the operator to run" read as an instruction for somebody else. The
// default stays the wording safe to read from either side, so a caller that
// does not say who it is keeps handing the command on.
func TestRevealAsSaysWhatToRunToThePersonAtTheTerminal(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	const missing = "staging-db-password"

	for _, c := range []struct {
		sf   plugin.Surface
		want string
	}{
		{plugin.SurfaceCLI, "`rta kv set staging-db-password` creates the store, locked with a passphrase you choose, and the entry"},
		{plugin.SurfaceTUI, "`kv.set key=staging-db-password` creates the store, locked with a passphrase you choose, and the entry"},
	} {
		_, verr := RevealAs(c.sf)(missing)
		if verr == nil || verr.Hint != c.want {
			t.Errorf("%s, no store: hint = %+v, want %q", c.sf, verr, c.want)
		}
	}
	if _, verr := Reveal(missing); verr == nil || !strings.HasPrefix(verr.Hint, plugin.AskOperator("kv set "+missing)) {
		t.Errorf("the default no longer hands the command on as the operator's: %+v", verr)
	}

	text(t, runSet, map[string]any{"key": "another-entry", "value": "x"}, false)
	for sf, want := range map[plugin.Surface]string{
		plugin.SurfaceCLI: "`rta kv list` lists every key",
		plugin.SurfaceTUI: "`kv.list` lists every key",
	} {
		_, verr := RevealAs(sf)(missing)
		if verr == nil || verr.Hint != want {
			t.Errorf("%s, a store without the entry: hint = %+v, want %q", sf, verr, want)
		}
		if verr != nil && strings.Contains(verr.Hint+verr.Message, "another-entry") {
			t.Errorf("%s: the refusal names an entry that does exist: %+v", sf, verr)
		}
	}
}

// The form's box and --passphrase hand over one answer for a store not made
// yet, so the call that makes it says so: the receipt for the first entry was
// the same line as for the tenth, and the person learned there was a store
// whose passphrase nothing recovers from the first call that would not open it.
func TestTheCallThatMakesTheStoreSaysSo(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	first := text(t, runSet, map[string]any{"key": "db-password", "value": "s3cr3t"}, false)
	if !strings.Contains(first, "made a new store") || !strings.Contains(first, "nothing recovers it") {
		t.Errorf("the call that made the store says nothing of it: %q", first)
	}
	second := text(t, runSet, map[string]any{"key": "other-key", "value": "x"}, false)
	if strings.Contains(second, "new store") {
		t.Errorf("a later call claims to have made the store: %q", second)
	}
}

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

// The four capabilities that put a stored value in front of somebody declare
// it, so the host can say so to a caller and a masked view can point at them
// (plugin.Capability.Reveals). Declaring it is coupled to the gate at
// registration, so a reveal that dropped its grant or its scope would fail to
// load, and one that stopped declaring it would fail here: what the other
// capabilities in the namespace show — a list of names, a status, the history
// of a key — is deliberately not a value.
func TestTheCapabilitiesThatShowAStoredValueDeclareThatTheyReveal(t *testing.T) {
	reveals := map[string]bool{"kv.get": true, "kv.env": true, "kv.copy": true, "kv.edit": true}
	for _, c := range Plugin().Capabilities {
		if c.Reveals != reveals[c.ID] {
			t.Errorf("%s declares Reveals = %v, want %v", c.ID, c.Reveals, reveals[c.ID])
		}
		delete(reveals, c.ID)
	}
	if len(reveals) > 0 {
		t.Errorf("capabilities that were expected and are not declared: %v", reveals)
	}
}

func TestStoreStampOfAMissingStoreIsEmpty(t *testing.T) {
	setup(t)
	if got := StoreStamp(); got != "" {
		t.Errorf("StoreStamp = %q, want empty for a store that does not exist", got)
	}
}
