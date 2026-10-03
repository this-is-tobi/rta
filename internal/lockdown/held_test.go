package lockdown

import (
	"encoding/json"
	"testing"
)

// The lock a process with nothing verified holds a principal to was told from
// a placed one by its By text, and By is the operator's word: a lock placed
// over the operator channel by a label spelled as that text read as one no
// operator could lift, and was refused with the sentence for a lock file that
// does not verify. Whether a lock is the held kind is a fact about how it was
// made and nothing the file can say.
func TestALockPlacedWithTheMarkersTextIsNotTheHeldKind(t *testing.T) {
	placed := Lock{Kind: KindAgent, Name: "claude", By: heldBy}
	if placed.Held() {
		t.Fatal("a lock somebody placed reads as the one nothing placed")
	}
	if got := Refusal(&placed); got.Code != "core.lock.frozen" {
		t.Fatalf("refused as %q, want core.lock.frozen", got.Code)
	}

	raw, err := json.Marshal(*heldFor(KindAgent, "claude"))
	if err != nil {
		t.Fatal(err)
	}
	var round Lock
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatal(err)
	}
	if round.Held() {
		t.Fatal("a lock read from a file can claim to be the held kind")
	}

	if !heldFor(KindAgent, "claude").Held() {
		t.Fatal("the lock held for a principal does not say it is")
	}
}
