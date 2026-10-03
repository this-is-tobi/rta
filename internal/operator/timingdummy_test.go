package operator

import (
	"crypto/ed25519"
	"testing"
)

// The key Verify burns a check against for an unknown fingerprint is the
// public half of the all-zero seed, and a point ed25519.Verify decodes. The
// second half is what matters: a constant that is not on the curve is
// rejected before any curve work happens, and an unknown fingerprint would
// then answer measurably faster than a wrong signature from an enrolled one.
// A signature made with the seed verifying under the constant proves both
// that it is that key and that Verify gets past decoding it.
func TestTheTimingDummyIsAPointVerifyDecodes(t *testing.T) {
	private := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	msg := []byte("a challenge nobody enrolled a key for")
	if !ed25519.Verify(timingDummy, msg, ed25519.Sign(private, msg)) {
		t.Fatal("timingDummy is not the public half of the all-zero seed, or not a point Verify can decode")
	}
}
