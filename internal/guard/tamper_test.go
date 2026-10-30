package guard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// A guard whose verification key was swapped for one the passphrase does not
// unlock would honour the swapped key's signatures while the real passphrase
// went on unlocking without a word. The unlock says so instead, before the
// operator signs anything into a file that now trusts someone else.
func TestUnlockRefusesAVerificationKeyItsPassphraseDoesNotUnlock(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, verr := Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	outside, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	st["publicKey"] = base64.StdEncoding.EncodeToString(outside)
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), out, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, verr := Unlock("correct horse"); verr == nil || verr.Code != "core.guard.tampered" {
		t.Fatalf("Unlock over a swapped verification key = %v, want core.guard.tampered", verr)
	}
	if _, verr := Unlock("battery staple"); verr == nil || verr.Code != "core.guard.passphrase" {
		t.Fatalf("Unlock with the wrong passphrase over the swapped file = %v, want core.guard.passphrase", verr)
	}
}
