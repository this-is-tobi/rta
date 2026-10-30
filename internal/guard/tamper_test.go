package guard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"
)

// readState is the guard's state file as JSON, for a test to change a field.
func readState(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

// writeState writes st over the guard's state file, as a process that can
// write the data directory would.
func writeState(t *testing.T, st map[string]any) {
	t.Helper()
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), out, 0o600); err != nil {
		t.Fatal(err)
	}
}

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
	st := readState(t)
	st["publicKey"] = base64.StdEncoding.EncodeToString(outside)
	writeState(t, st)

	if _, verr := Unlock("correct horse"); verr == nil || verr.Code != "core.guard.tampered" {
		t.Fatalf("Unlock over a swapped verification key = %v, want core.guard.tampered", verr)
	}
	if _, verr := Unlock("battery staple"); verr == nil || verr.Code != "core.guard.passphrase" {
		t.Fatalf("Unlock with the wrong passphrase over the swapped file = %v, want core.guard.passphrase", verr)
	}
}

// A verification key that is not an ed25519 one makes a state that does not
// parse, in either mode: no fingerprint for status and doctor to show as a
// key, and the corrupt-state refusal from every reader, where status used to
// print a fingerprint for a key no signature could verify against.
func TestAVerificationKeyThatIsNotAKeyIsACorruptState(t *testing.T) {
	short := base64.StdEncoding.EncodeToString(make([]byte, 31))
	for label, enable := range map[string]func(t *testing.T){
		"local": func(t *testing.T) {
			if _, verr := Enable("correct horse"); verr != nil {
				t.Fatal(verr)
			}
			st := readState(t)
			st["publicKey"] = short
			writeState(t, st)
		},
		"remote": func(t *testing.T) {
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			ops := []OperatorKey{{Label: "alice", PublicKey: base64.StdEncoding.EncodeToString(pub)}}
			if verr := EnableRemote(ops, "https://rta.example.com"); verr != nil {
				t.Fatal(verr)
			}
			st := readState(t)
			st["operators"] = []map[string]string{{"label": "alice", "publicKey": short}}
			writeState(t, st)
		},
	} {
		t.Run(label, func(t *testing.T) {
			t.Setenv("RTA_DATA_DIR", t.TempDir())
			enable(t)
			if fp := Fingerprint(); fp != "" {
				t.Errorf("Fingerprint = %q for a key that is not one", fp)
			}
			if _, verr := Verifier(); verr == nil || verr.Code != "core.guard.corrupt" {
				t.Errorf("Verifier = %v, want core.guard.corrupt", verr)
			}
		})
	}
}
