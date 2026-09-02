package passkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
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
