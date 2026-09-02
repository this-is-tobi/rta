package guard

import (
	"os"
	"strings"
	"testing"
)

// TestMain lowers the passphrase KDF cost for the whole package, exactly as
// builtin/kv's suite does: the default work factor is the point in
// production and a tax in a loop.
func TestMain(m *testing.M) {
	ScryptWorkFactor = 10
	os.Exit(m.Run())
}

func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func TestEnableSignVerifyRoundTrip(t *testing.T) {
	isolate(t)
	if Enabled() {
		t.Fatal("enabled before anything was")
	}
	s, verr := Enable("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	if !Enabled() {
		t.Fatal("not enabled after Enable")
	}
	msg := []byte(`{"target":"kv.get"}`)
	sig := s.Sign(msg)
	if !Verify(msg, sig) {
		t.Fatal("a fresh signature does not verify")
	}
	if Verify([]byte(`{"target":"kv.rm"}`), sig) {
		t.Fatal("a signature verified over different bytes")
	}
	if Verify(msg, "") {
		t.Fatal("an empty signature verified")
	}
}

func TestUnlockNeedsTheRightPassphrase(t *testing.T) {
	isolate(t)
	if _, verr := Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := Unlock("wrong horse"); verr == nil {
		t.Fatal("a wrong passphrase unlocked the key")
	} else if verr.Code != "core.guard.passphrase" {
		t.Fatalf("code = %s, want core.guard.passphrase", verr.Code)
	}
	s, verr := Unlock("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	msg := []byte("payload")
	if !Verify(msg, s.Sign(msg)) {
		t.Fatal("the unlocked key does not sign what Enable's did")
	}
}

func TestEnableRefusesOverExistingState(t *testing.T) {
	isolate(t)
	if _, verr := Enable("one"); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := Enable("two"); verr == nil {
		t.Fatal("a second Enable rotated the key silently")
	} else if verr.Code != "core.guard.exists" {
		t.Fatalf("code = %s, want core.guard.exists", verr.Code)
	}
}

func TestDisableProvesThePassphraseFirst(t *testing.T) {
	isolate(t)
	if _, verr := Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	if verr := Disable("wrong horse"); verr == nil {
		t.Fatal("the guard came off without its passphrase")
	}
	if !Enabled() {
		t.Fatal("a refused disable still removed the state")
	}
	if verr := Disable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	if Enabled() {
		t.Fatal("still enabled after a proven disable")
	}
}

// A state file that exists but does not parse must read as enabled and
// verify nothing: Enabled=true only refuses things, so corruption lands
// closed rather than as "the guard was never set up".
func TestCorruptStateFailsClosed(t *testing.T) {
	isolate(t)
	if err := os.WriteFile(Path(), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Enabled() {
		t.Fatal("a corrupt state read as disabled")
	}
	if Verify([]byte("anything"), "sig") {
		t.Fatal("a corrupt state verified a signature")
	}
	if _, verr := Unlock("any"); verr == nil {
		t.Fatal("a corrupt state unlocked")
	} else if verr.Code != "core.guard.corrupt" {
		t.Fatalf("code = %s, want core.guard.corrupt", verr.Code)
	}
}

func TestFingerprintIsStableAndShort(t *testing.T) {
	isolate(t)
	if fp := Fingerprint(); fp != "" {
		t.Fatalf("a fingerprint with no guard: %q", fp)
	}
	if _, verr := Enable("p"); verr != nil {
		t.Fatal(verr)
	}
	a, b := Fingerprint(), Fingerprint()
	if a == "" || a != b {
		t.Fatalf("fingerprint unstable: %q vs %q", a, b)
	}
	if len(a) != 8 || strings.ToLower(a) != a {
		t.Fatalf("fingerprint %q is not eight lowercase hex characters", a)
	}
}

// The two-rm rollback — deleting guard.json and grants.json together — is
// invisible on disk: the machine reads as one where the guard was never
// enabled. The Pin is a running process's memory of the guard it started
// under, and the one place that rollback cannot reach.
func TestThePinCatchesTheRollback(t *testing.T) {
	isolate(t)
	if _, verr := Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	pin := TakePin()
	if verr := pin.Check(); verr != nil {
		t.Fatalf("a consistent state failed its own pin: %v", verr)
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	verr := pin.Check()
	if verr == nil {
		t.Fatal("the rollback passed the pin")
	}
	if verr.Code != "core.guard.pinned" {
		t.Fatalf("code = %s, want core.guard.pinned", verr.Code)
	}
}

// A key swapped for the attacker's own is a weakening too: the fingerprint
// the pin took no longer matches, whatever the new state verifies.
func TestThePinCatchesAKeySwap(t *testing.T) {
	isolate(t)
	if _, verr := Enable("original"); verr != nil {
		t.Fatal(verr)
	}
	pin := TakePin()
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if _, verr := Enable("attacker"); verr != nil {
		t.Fatal(verr)
	}
	if verr := pin.Check(); verr == nil {
		t.Fatal("a swapped key passed the pin")
	}
}

// The off→on direction is an operator strengthening their machine: a pin
// taken with no guard checks nothing, ever.
func TestAPinTakenWithNoGuardNeverRefuses(t *testing.T) {
	isolate(t)
	pin := TakePin()
	if verr := pin.Check(); verr != nil {
		t.Fatal(verr)
	}
	if _, verr := Enable("later"); verr != nil {
		t.Fatal(verr)
	}
	if verr := pin.Check(); verr != nil {
		t.Fatalf("enabling mid-session tripped the pin: %v", verr)
	}
}
