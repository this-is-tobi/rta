package passkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The scrypt default is the point in production and a tax here; 10 is the
// same floor the guard and kv suites use.
const testWork = 10

func testKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv
}

func TestWrapRoundTrips(t *testing.T) {
	priv := testKey(t)
	cipher, err := Wrap(priv, "correct horse", testWork)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unwrap(cipher, "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !priv.Equal(got) {
		t.Fatal("the unwrapped key is not the wrapped one")
	}
}

func TestAWrongPassphraseIsNamedAsSuch(t *testing.T) {
	cipher, err := Wrap(testKey(t), "correct horse", testWork)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unwrap(cipher, "wrong horse"); !errors.Is(err, ErrPassphrase) {
		t.Fatalf("err = %v, want ErrPassphrase", err)
	}
}

// Bytes that are not even base64 are an encoding problem, not a passphrase
// problem — the callers show "corrupt file" for one and "wrong passphrase"
// for the other, and conflating them strands an operator on the wrong hint.
func TestMangledCiphertextIsNotAPassphraseError(t *testing.T) {
	_, err := Unwrap("not!base64", "any")
	if err == nil {
		t.Fatal("mangled ciphertext unwrapped")
	}
	if errors.Is(err, ErrPassphrase) {
		t.Fatal("an encoding failure reads as a wrong passphrase")
	}
}

// Prompt's own comment makes its MCP check a wall a fourth caller must
// meet, not a channel: the invariant that no MCP caller can supply a
// passphrase holds there rather than by the grace of every capability that
// declares the field — and its refusal was the one surface wall in the tree
// nothing exercised. Both of Prompt's callers, the grant guard and the
// operator key, sit behind it; a value in the request must not get past it
// either, since a value is exactly what a tool call would carry.
func TestAPassphraseIsNeverAskedOnTheMCPSurface(t *testing.T) {
	text := PromptText{Subject: "the key", Prompt: "p: ", Codes: "test.passphrase", Empty: "e"}
	for _, values := range []map[string]any{{"passphrase": "x"}, nil} {
		req := plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceMCP)
		got, verr := Prompt(req, false, text)
		if verr == nil || verr.Code != "test.passphrase.surface" || !verr.Refusal || got != "" {
			t.Fatalf("Prompt on MCP with %v = %q, %v — want an empty answer and a surface refusal", values, got, verr)
		}
	}
}

// The floor is on setting a passphrase and counts what a person typed, as
// characters: seven letters are short and eight are not, and a passphrase of
// eight characters in four bytes' worth of script is not short for being
// small in bytes, nor one of three long characters long for being big in them.
func TestANewPassphraseNeedsEightCharacters(t *testing.T) {
	for _, c := range []struct {
		passphrase string
		short      bool
	}{
		{"", true},
		{"1234567", true},
		{"12345678", false},
		{"pässwörd", false},
		{"日本語のパスワード", false},
		{"日本語", true},
		{"👍👍👍👍👍👍👍", true},
		{"👍👍👍👍👍👍👍👍", false},
	} {
		verr := CheckNew(c.passphrase, "test.passphrase", "the key")
		if c.short != (verr != nil) {
			t.Errorf("CheckNew(%q) = %v, want short=%v", c.passphrase, verr, c.short)
		}
		if verr != nil && verr.Code != "test.passphrase.short" {
			t.Errorf("CheckNew(%q) code = %q, want test.passphrase.short", c.passphrase, verr.Code)
		}
	}
}

// The refusal says why and what to do, and offers no way round it: a
// passphrase this short falls to guessing from a copy of the file whatever
// scrypt costs per try, and a flag that waived that would be the flag every
// script passes.
func TestTheRefusalOfAShortPassphraseGivesItsReasonAndNoWayRound(t *testing.T) {
	verr := CheckNew("abc", "core.guard.passphrase", "the guard")
	if verr == nil {
		t.Fatal("a three-character passphrase was accepted")
	}
	for _, want := range []string{"the guard", "3 characters", "8", "offline"} {
		if !strings.Contains(verr.Message, want) {
			t.Errorf("message %q does not say %q", verr.Message, want)
		}
	}
	if strings.Contains(verr.Hint, "--") || strings.Contains(verr.Message, "--") {
		t.Errorf("the refusal names a flag: %q / %q", verr.Message, verr.Hint)
	}
}

// Prompt applies the floor only when it is asked to confirm, which is its one
// mark of a passphrase being set: the same flow, unconfirmed, is the entering
// of one that exists, and a key wrapped under a shorter passphrase before the
// floor was there has to keep opening. The TUI's masked field takes the same
// route out of Prompt as the prompt does, so it is held to the same floor.
func TestPromptHoldsANewPassphraseToTheFloorAndAnExistingOneToNothing(t *testing.T) {
	text := PromptText{Subject: "the key", Prompt: "p: ", Codes: "test.passphrase", Empty: "e"}
	tui := plugin.NewRequest(map[string]any{"passphrase": "short"}, false, false).WithSurface(plugin.SurfaceTUI)

	if got, verr := Prompt(tui, true, text); verr == nil || verr.Code != "test.passphrase.short" || got != "" {
		t.Errorf("a new five-character passphrase from the TUI field = %q, %v, want test.passphrase.short", got, verr)
	}
	if got, verr := Prompt(tui, false, text); verr != nil || got != "short" {
		t.Errorf("entering an existing five-character passphrase = %q, %v, want it handed back", got, verr)
	}
	long := plugin.NewRequest(map[string]any{"passphrase": "long enough"}, false, false).WithSurface(plugin.SurfaceTUI)
	if got, verr := Prompt(long, true, text); verr != nil || got != "long enough" {
		t.Errorf("a new eleven-character passphrase = %q, %v, want it accepted", got, verr)
	}
}

// A key already wrapped under a short passphrase opens, because the floor
// belongs to setting one and Unwrap is using one.
func TestAKeyWrappedUnderAShortPassphraseStillOpens(t *testing.T) {
	priv := testKey(t)
	cipher, err := Wrap(priv, "abc", testWork)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Unwrap(cipher, "abc")
	if err != nil || !priv.Equal(got) {
		t.Fatalf("Unwrap under the short passphrase = %v, %v", got, err)
	}
}
