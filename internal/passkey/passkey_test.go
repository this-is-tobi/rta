package passkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
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
