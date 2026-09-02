package guard

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func remoteKey(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(pub), priv
}

func TestARemoteGuardTrustsExactlyItsOperators(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	pub, priv := remoteKey(t)
	if verr := EnableRemote([]OperatorKey{{Label: "tobi", PublicKey: pub}}, "https://a.example"); verr != nil {
		t.Fatal(verr)
	}
	if !Enabled() || !Remote() {
		t.Fatal("the remote guard does not read as an enabled remote guard")
	}
	msg := []byte("authority bytes")
	if !Verify(msg, SignerFor(priv).Sign(msg)) {
		t.Fatal("the enrolled operator's signature was refused")
	}
	_, stranger := remoteKey(t)
	if Verify(msg, SignerFor(stranger).Sign(msg)) {
		t.Fatal("a stranger's signature verified")
	}
	if got := OperatorLabels(); len(got) != 1 || got[0] != "tobi" {
		t.Fatalf("OperatorLabels = %v", got)
	}
}

// On a remote server, "nothing here can unlock the guard" is the boundary
// working: there is no ciphertext, no passphrase, and no local issuance.
func TestARemoteGuardHasNothingToUnlock(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	pub, _ := remoteKey(t)
	if verr := EnableRemote([]OperatorKey{{Label: "tobi", PublicKey: pub}}, "https://a.example"); verr != nil {
		t.Fatal(verr)
	}
	_, verr := Unlock("any passphrase at all")
	if verr == nil {
		t.Fatal("a remote guard unlocked")
	}
	if verr.Code != "core.guard.remote" {
		t.Fatalf("code = %s, want core.guard.remote", verr.Code)
	}
}

// The Pin watches the fingerprint, so the enrolled set changing under a
// running server must move it the way a swapped local key does.
func TestTheRemoteFingerprintTracksTheEnrolledSet(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	pubA, _ := remoteKey(t)
	pubB, _ := remoteKey(t)
	if verr := EnableRemote([]OperatorKey{{Label: "a", PublicKey: pubA}}, "https://a.example"); verr != nil {
		t.Fatal(verr)
	}
	one := Fingerprint()
	if verr := DisableRemote(); verr != nil {
		t.Fatal(verr)
	}
	if verr := EnableRemote([]OperatorKey{
		{Label: "a", PublicKey: pubA}, {Label: "b", PublicKey: pubB},
	}, "https://a.example"); verr != nil {
		t.Fatal(verr)
	}
	if two := Fingerprint(); one == "" || two == "" || one == two {
		t.Fatalf("fingerprints %q and %q do not distinguish the sets", one, two)
	}
}

// DisableRemote must never become the passphrase-free way off a *local*
// guard — that would be the exact bypass Disable exists to price.
func TestDisableRemoteRefusesALocalGuard(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if _, verr := Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
	verr := DisableRemote()
	if verr == nil {
		t.Fatal("a local guard was disabled without its passphrase")
	}
	if !Enabled() {
		t.Fatal("the refusal still removed the guard")
	}
}

func TestARemoteGuardRefusesAnEmptyOrGarbledEnrollment(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if verr := EnableRemote(nil, "https://a.example"); verr == nil {
		t.Fatal("an empty enrollment enabled a guard nobody can satisfy")
	}
	if verr := EnableRemote([]OperatorKey{{Label: "x", PublicKey: "not-a-key"}}, "https://a.example"); verr == nil {
		t.Fatal("a garbled key enrolled")
	}
	if Enabled() {
		t.Fatal("a refused enrollment left state behind")
	}
}
