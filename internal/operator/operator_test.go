package operator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// The scrypt default is the point in production and a tax here.
	ScryptWorkFactor = 10
	os.Exit(m.Run())
}

func freshHome(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestInitUnlockRoundTrips(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	if s.Fingerprint() == "" || s.Fingerprint() != Fingerprint() {
		t.Fatalf("signer fingerprint %q does not match the stored key's %q", s.Fingerprint(), Fingerprint())
	}
	again, verr := Unlock("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	if again.Fingerprint() != s.Fingerprint() {
		t.Fatal("unlock produced a different key")
	}
}

func TestAWrongPassphraseIsNamedAsSuch(t *testing.T) {
	freshHome(t)
	if _, verr := Init("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Unlock("wrong horse")
	if verr == nil {
		t.Fatal("a wrong passphrase unlocked the key")
	}
	if verr.Code != "core.operator.passphrase" {
		t.Fatalf("code = %s, want core.operator.passphrase", verr.Code)
	}
}

func TestInitRefusesOverAnExistingKey(t *testing.T) {
	freshHome(t)
	if _, verr := Init("one"); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Init("two")
	if verr == nil {
		t.Fatal("a second init overwrote the key")
	}
	if verr.Code != "core.operator.exists" {
		t.Fatalf("code = %s, want core.operator.exists", verr.Code)
	}
}

func writeRoster(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "operators")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The line `rta operator status` prints is the line a roster reads — the two
// ends of the enrollment flow must agree on the format or the flow is a doc
// that lies.
func TestTheRosterLineEnrolls(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, verr := RosterLine("tobi")
	if verr != nil {
		t.Fatal(verr)
	}
	roster, _, err := LoadRoster(writeRoster(t, "# the ops team\n"+line+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	env := s.Sign("nonce-1", VerbStatus, nil)
	label, ok := roster.Verify(env)
	if !ok || label != "tobi" {
		t.Fatalf("Verify = %q, %v — want tobi, true", label, ok)
	}
}

func TestVerifyRefusesWhatTheRosterNeverEnrolled(t *testing.T) {
	freshHome(t)
	s, verr := Init("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	line, _ := RosterLine("tobi")
	roster, _, err := LoadRoster(writeRoster(t, line+"\n"))
	if err != nil {
		t.Fatal(err)
	}

	env := s.Sign("nonce-1", VerbGrantList, []byte(`{"a":1}`))
	if _, ok := roster.Verify(env); !ok {
		t.Fatal("the enrolled key was refused")
	}

	tampered := env
	tampered.Payload = []byte(`{"a":2}`)
	if _, ok := roster.Verify(tampered); ok {
		t.Fatal("a tampered payload verified")
	}
	replayedVerb := env
	replayedVerb.Verb = VerbStatus
	if _, ok := roster.Verify(replayedVerb); ok {
		t.Fatal("a signature travelled to a different verb")
	}
	otherNonce := env
	otherNonce.Nonce = "nonce-2"
	if _, ok := roster.Verify(otherNonce); ok {
		t.Fatal("a signature travelled to a different nonce")
	}

	// A different keypair, never enrolled, claiming the enrolled fingerprint.
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	stranger, verr := Init("other pass")
	if verr != nil {
		t.Fatal(verr)
	}
	forged := stranger.Sign("nonce-1", VerbGrantList, []byte(`{"a":1}`))
	forged.Fingerprint = env.Fingerprint
	if _, ok := roster.Verify(forged); ok {
		t.Fatal("an un-enrolled key verified")
	}
}

func TestTheRosterRefusesWeakPermissions(t *testing.T) {
	path := writeRoster(t, "tobi AAAA\n")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadRoster(path); err == nil {
		t.Fatal("a world-readable roster loaded")
	}
}

func TestTheRosterNamesAGarbledKey(t *testing.T) {
	if _, _, err := LoadRoster(writeRoster(t, "tobi not-a-key\n")); err == nil {
		t.Fatal("a garbled key loaded")
	}
	if _, _, err := LoadRoster(writeRoster(t, "# nobody\n")); err == nil {
		t.Fatal("an empty roster loaded")
	}
}

func TestOneKeyEnrollsOnce(t *testing.T) {
	freshHome(t)
	if _, verr := Init("p"); verr != nil {
		t.Fatal(verr)
	}
	line, _ := RosterLine("tobi")
	other, _ := RosterLine("also-tobi")
	if _, _, err := LoadRoster(writeRoster(t, line+"\n"+other+"\n")); err == nil {
		t.Fatal("the same key enrolled under two labels")
	}
}

func TestANonceSpendsExactlyOnce(t *testing.T) {
	n := NewNonces(0)
	nonce, err := n.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !n.Consume(nonce) {
		t.Fatal("a fresh nonce did not spend")
	}
	if n.Consume(nonce) {
		t.Fatal("a nonce spent twice")
	}
	if n.Consume("never-issued") {
		t.Fatal("an invented nonce spent")
	}
}

func TestAnExpiredNonceDoesNotSpend(t *testing.T) {
	n := NewNonces(time.Nanosecond)
	nonce, err := n.Issue()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if n.Consume(nonce) {
		t.Fatal("an expired nonce spent")
	}
}
