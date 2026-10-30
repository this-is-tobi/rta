package operator

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/passkey"
)

// swapPublicKey rewrites the state file's verification key, as a process that
// can write the data directory would, and returns the key it put there.
func swapPublicKey(t *testing.T, path string) ed25519.PublicKey {
	t.Helper()
	outside, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	writePublicKey(t, path, base64.StdEncoding.EncodeToString(outside))
	return outside
}

// writePublicKey puts encoded where the state file keeps its verification key.
func writePublicKey(t *testing.T, path, encoded string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	st["publicKey"] = encoded
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// The unlock is held to the file: a verification key the passphrase does not
// unlock is a file changed outside rta, and the refusal names both keys by
// fingerprint, so the operator can tell which roster line is theirs.
func TestUnlockRefusesAVerificationKeyItsPassphraseDoesNotUnlock(t *testing.T) {
	freshHome(t)
	real, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	outside := swapPublicKey(t, Path())

	_, verr = Unlock("correct horse")
	if verr == nil || verr.Code != "core.operator.tampered" {
		t.Fatalf("Unlock over a swapped verification key = %v, want core.operator.tampered", verr)
	}
	for _, fp := range []string{real.Fingerprint(), passkey.Fingerprint(outside)} {
		if !strings.Contains(verr.Message+verr.Hint, fp) {
			t.Errorf("the refusal does not name fingerprint %s: %s — %s", fp, verr.Message, verr.Hint)
		}
	}
}

// A wrong passphrase is still a wrong passphrase over a swapped file: the
// comparison needs the unwrapped key, so it can only follow a good unwrap.
func TestAWrongPassphraseOverASwappedFileIsStillTheWrongPassphrase(t *testing.T) {
	freshHome(t)
	if _, verr := Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	swapPublicKey(t, Path())
	if _, verr := Unlock("battery staple"); verr == nil || verr.Code != "core.operator.passphrase" {
		t.Fatalf("Unlock with the wrong passphrase = %v, want core.operator.passphrase", verr)
	}
}

// A verification key that is not an ed25519 one makes a file that does not
// parse: no fingerprint, no roster line and no unlock, where it used to get a
// fingerprint of whatever bytes it decoded to and look like a key.
func TestAVerificationKeyThatIsNotAKeyIsACorruptFile(t *testing.T) {
	for label, encoded := range map[string]string{
		"31 bytes":   base64.StdEncoding.EncodeToString(make([]byte, 31)),
		"64 bytes":   base64.StdEncoding.EncodeToString(make([]byte, 64)),
		"not base64": "not a key",
	} {
		t.Run(label, func(t *testing.T) {
			freshHome(t)
			if _, verr := Init("correct horse"); verr != nil {
				t.Fatal(verr)
			}
			writePublicKey(t, Path(), encoded)
			if fp := Fingerprint(); fp != "" {
				t.Errorf("Fingerprint = %q for a key that is not one", fp)
			}
			if _, verr := RosterLine("alice"); verr == nil || verr.Code != "core.operator.corrupt" {
				t.Errorf("RosterLine = %v, want core.operator.corrupt", verr)
			}
			if _, verr := Unlock("correct horse"); verr == nil || verr.Code != "core.operator.corrupt" {
				t.Errorf("Unlock = %v, want core.operator.corrupt", verr)
			}
		})
	}
}
