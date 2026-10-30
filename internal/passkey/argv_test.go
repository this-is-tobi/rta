package passkey

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Eight hex characters, the same for the same key: the fingerprint a client
// prints is the string a server's roster looks up, so it has to be one
// function and one answer.
func TestAFingerprintIsEightHexCharactersOfTheKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(pub)
	if !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(fp) {
		t.Errorf("Fingerprint = %q, want eight lowercase hex characters", fp)
	}
	if again := Fingerprint(pub); again != fp {
		t.Errorf("the same key fingerprinted twice: %q then %q", fp, again)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if Fingerprint(other) == fp {
		t.Error("two keys share a fingerprint")
	}
}

// A passphrase on argv is readable by every process and kept by shell
// history, so a CLI call that carries one is refused whether or not anything
// would read it. The other surfaces have channels that land nowhere, and an
// empty flag is not a passphrase.
func TestAPassphraseOnTheCommandLineIsRefusedOnlyThere(t *testing.T) {
	cli := plugin.NewRequest(map[string]any{"passphrase": "hunter2"}, false, false).WithSurface(plugin.SurfaceCLI)
	if verr := Argv(cli, "core.guard"); verr == nil || verr.Code != "core.guard.argv" {
		t.Errorf("CLI with a passphrase: %v, want core.guard.argv", verr)
	}
	blank := plugin.NewRequest(map[string]any{"passphrase": "  "}, false, false).WithSurface(plugin.SurfaceCLI)
	if verr := Argv(blank, "core.guard"); verr != nil {
		t.Errorf("CLI with a blank passphrase: %v, want nothing refused", verr)
	}
	tui := plugin.NewRequest(map[string]any{"passphrase": "hunter2"}, false, false).WithSurface(plugin.SurfaceTUI)
	if verr := Argv(tui, "core.guard"); verr != nil {
		t.Errorf("TUI with a passphrase: %v, want nothing refused — its field is masked", verr)
	}
}

// A verification key is 32 bytes in the one spelling EncodeToString writes.
// The all-zero key's last character carries two bits no byte uses, so
// setting one gives a second spelling the lenient decoder reads as the same
// key, which is what the roster's one-label-per-key rule cannot have.
func TestAPublicKeyHasOneLengthAndOneSpelling(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := ParsePublicKey(base64.StdEncoding.EncodeToString(pub)); !ok || !got.Equal(pub) {
		t.Fatalf("a real key did not parse back to itself: %v, %v", got, ok)
	}
	zero := base64.StdEncoding.EncodeToString(make([]byte, ed25519.PublicKeySize))
	second := strings.TrimSuffix(zero, "A=") + "B="
	if lenient, err := base64.StdEncoding.DecodeString(second); err != nil || len(lenient) != ed25519.PublicKeySize {
		t.Fatalf("the second spelling is not one the lenient decoder reads: %v", err)
	}
	for label, encoded := range map[string]string{
		"31 bytes":          base64.StdEncoding.EncodeToString(make([]byte, 31)),
		"33 bytes":          base64.StdEncoding.EncodeToString(make([]byte, 33)),
		"a second spelling": second,
		"not base64":        "not a key at all",
		"empty":             "",
	} {
		if _, ok := ParsePublicKey(encoded); ok {
			t.Errorf("%s parsed as a verification key", label)
		}
	}
}
