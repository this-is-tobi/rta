package kv

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The person's own unlock: a passphrase typed into the TUI's unlock form, tried
// against the store, and held for the length they chose. These tests pin what
// makes that safe to offer — a wrong one is never held, a tile reading the store
// on a timer cannot keep it alive, the length is bounded, and it asks nothing
// where there is nothing to ask.

const aPassphrase = "correct horse battery staple"

func aStoreLockedWith(t *testing.T, passphrase string) {
	t.Helper()
	text(t, runSet, map[string]any{"key": "k", "value": "v", "passphrase": passphrase}, false)
}

func TestAnUnlockOpensTheStoreForAProfilesCredentialUntilItLapses(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	aStoreLockedWith(t, aPassphrase)

	if _, verr := Reveal("k"); verr == nil {
		t.Fatal("the store opened with nothing unlocked")
	}
	if verr := StartSession(aPassphrase, 30*time.Minute); verr != nil {
		t.Fatalf("StartSession: %+v", verr)
	}
	if got, verr := Reveal("k"); verr != nil || got != "v" {
		t.Fatalf("Reveal after an unlock = %q, %+v", got, verr)
	}
	until, ok := SessionUntil()
	if !ok || !until.Equal(at.Add(30*time.Minute)) {
		t.Errorf("SessionUntil = %v, %v; want %v", until, ok, at.Add(30*time.Minute))
	}
	*at = at.Add(31 * time.Minute)
	if _, verr := Reveal("k"); verr == nil {
		t.Error("the store was still open after the length the person chose")
	}
}

// Tiles read a profile's credential every few seconds with nobody there. If
// that counted as use the session could never end, which is the one thing a
// session left open on a desk has to do.
func TestATileReadingTheStoreDoesNotKeepTheUnlockAlive(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	aStoreLockedWith(t, aPassphrase)
	if verr := StartSession(aPassphrase, 10*time.Minute); verr != nil {
		t.Fatal(verr)
	}
	for range 9 {
		*at = at.Add(time.Minute)
		if _, verr := Reveal("k"); verr != nil {
			t.Fatalf("Reveal inside the window: %+v", verr)
		}
	}
	until, _ := SessionUntil()
	if !until.Equal(time.Date(2026, 9, 13, 12, 10, 0, 0, time.UTC)) {
		t.Errorf("the deadline moved to %v after nine reads", until)
	}
	*at = at.Add(2 * time.Minute)
	if _, verr := Reveal("k"); verr == nil {
		t.Error("reads kept the session open past its length")
	}
}

// A person's own use of a kv action does count, as it always did, and by the
// length they chose.
func TestAPersonsOwnUseMovesTheDeadlineByTheLengthTheyChose(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	aStoreLockedWith(t, aPassphrase)
	if verr := StartSession(aPassphrase, 2*time.Hour); verr != nil {
		t.Fatal(verr)
	}
	*at = at.Add(time.Hour)
	if _, ok := SessionPassphrase(); !ok {
		t.Fatal("the session ended inside its length")
	}
	until, _ := SessionUntil()
	if !until.Equal(at.Add(2 * time.Hour)) {
		t.Errorf("deadline = %v, want two hours from the use", until)
	}
}

func TestAWrongPassphraseIsRefusedAndNotHeld(t *testing.T) {
	setup(t)
	freezeClock(t)
	aStoreLockedWith(t, aPassphrase)

	verr := StartSession("not it", time.Hour)
	if verr == nil || verr.Code != "kv.wrongpass" {
		t.Fatalf("verr = %+v, want kv.wrongpass", verr)
	}
	if _, ok := SessionUntil(); ok {
		t.Error("a passphrase that did not open the store was held")
	}
}

func TestTheLengthOfAnUnlockIsBounded(t *testing.T) {
	setup(t)
	freezeClock(t)
	aStoreLockedWith(t, aPassphrase)
	for _, d := range []time.Duration{0, 30 * time.Second, MaxSessionTTL + time.Second} {
		verr := StartSession(aPassphrase, d)
		if verr == nil || verr.Code != "kv.session.ttl" {
			t.Errorf("%s: verr = %+v, want kv.session.ttl", d, verr)
		}
	}
	if verr := StartSession(aPassphrase, MaxSessionTTL); verr != nil {
		t.Errorf("the ceiling itself was refused: %+v", verr)
	}
}

func TestThereIsNothingToUnlockWithoutAStoreOrWhenTheEnvironmentOpensIt(t *testing.T) {
	setup(t)
	freezeClock(t)
	if _, ok := UnlockPrompt(); ok {
		t.Error("a prompt was offered with no store")
	}
	if verr := StartSession(aPassphrase, time.Hour); verr == nil || verr.Code != "kv.session.nothing" {
		t.Errorf("no store: verr = %+v", verr)
	}

	aStoreLockedWith(t, aPassphrase)
	if p, ok := UnlockPrompt(); !ok || p != "the store's passphrase" {
		t.Errorf("UnlockPrompt = %q, %v", p, ok)
	}
	t.Setenv(passphraseEnv, aPassphrase)
	if _, ok := UnlockPrompt(); ok {
		t.Error("a prompt was offered although the environment opens the store")
	}
	if verr := StartSession(aPassphrase, time.Hour); verr == nil || verr.Code != "kv.session.nothing" {
		t.Errorf("environment: verr = %+v", verr)
	}
}

func TestOnceUnlockedThereIsNothingMoreToAsk(t *testing.T) {
	setup(t)
	freezeClock(t)
	aStoreLockedWith(t, aPassphrase)
	if err := StartSession(aPassphrase, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, ok := UnlockPrompt(); ok {
		t.Error("a prompt was offered while the store is unlocked")
	}
	ForgetSession()
	if _, ok := UnlockPrompt(); !ok {
		t.Error("no prompt after the session was ended")
	}
}

// The case that started this: a store locked to an SSH key that has a
// passphrase of its own. The passphrase the person types is the key's, and the
// host's read feeds it to the key.
func TestAnUnlockOpensAStoreLockedToAPassphraseProtectedKey(t *testing.T) {
	_, configDir := setupWithConfig(t)
	freezeClock(t)
	path := writeLockedSSHKey(t, configDir, "kv.identity", "the key one")
	stubKeyPrompt(t, "the key one")
	if _, err := runSet(context.Background(), cliReq(map[string]any{"key": "k", "value": "v"})); err != nil {
		t.Fatalf("set with the default identity: %v", err)
	}
	keyPassphrases = map[string]string{}

	if _, verr := Reveal("k"); verr == nil || verr.Code != "kv.identity.locked" {
		t.Fatalf("Reveal with the key locked: %+v", verr)
	}
	prompt, ok := UnlockPrompt()
	if !ok || !strings.Contains(prompt, path) {
		t.Fatalf("UnlockPrompt = %q, %v; want it to name %s", prompt, ok, path)
	}
	if verr := StartSession("not the key's", time.Hour); verr == nil || verr.Code != "kv.identity.locked" {
		t.Errorf("a wrong key passphrase: %+v", verr)
	}
	if verr := StartSession("the key one", time.Hour); verr != nil {
		t.Fatalf("StartSession: %+v", verr)
	}
	if got, verr := Reveal("k"); verr != nil || got != "v" {
		t.Errorf("Reveal after the unlock = %q, %+v", got, verr)
	}
}

// The refusal a tile shows says what a person at the TUI can do about it, and
// an agent's reader never learns there is a key at all.
func TestTheTUIsRefusalNamesTheUnlockKey(t *testing.T) {
	setup(t)
	aStoreLockedWith(t, aPassphrase)
	_, verr := RevealAs(plugin.SurfaceTUI)("k")
	if verr == nil || !strings.Contains(verr.Hint, "press U") {
		t.Fatalf("verr = %+v, want a hint naming U", verr)
	}
	_, verr = Reveal("k")
	if verr == nil || strings.Contains(verr.Hint, "press U") {
		t.Errorf("the default reader was handed a TUI key: %+v", verr)
	}
}

// The bind cache is keyed by this stamp, and the store file does not change when
// a person unlocks it: without the session in the stamp the tiles kept the
// refusal they cached while it was shut.
func TestTheStoreStampMovesWhenTheSessionOpensAndWhenItLapses(t *testing.T) {
	setup(t)
	at := freezeClock(t)
	aStoreLockedWith(t, aPassphrase)

	shut := StoreStamp()
	if err := StartSession(aPassphrase, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	open := StoreStamp()
	if open == shut || !strings.HasSuffix(open, ":unlocked") {
		t.Errorf("stamp while unlocked = %q, shut = %q", open, shut)
	}
	if again := StoreStamp(); again != open {
		t.Errorf("looking at the stamp changed it: %q then %q", open, again)
	}
	*at = at.Add(6 * time.Minute)
	if lapsed := StoreStamp(); lapsed != shut {
		t.Errorf("stamp after the lapse = %q, want the shut one %q", lapsed, shut)
	}
}

func TestTheOverviewNamesTheUnlockKeyOnlyWhereAPersonCanPressIt(t *testing.T) {
	tui := plugin.NewRequest(nil, false, false).WithSurface(plugin.SurfaceTUI)
	if got := unlockWays(tui); !strings.Contains(got, "press U") {
		t.Errorf("TUI: %q", got)
	}
	for _, sf := range []plugin.Surface{plugin.SurfaceCLI, plugin.SurfaceMCP} {
		got := unlockWays(plugin.NewRequest(nil, false, false).WithSurface(sf))
		if strings.Contains(got, "press U") || !strings.Contains(got, passphraseEnv) {
			t.Errorf("%s: %q", sf, got)
		}
	}
}
