package guard

import (
	"fmt"
	"os"
	"testing"
)

// A roster too large for the guard to read back was written all the same: the
// read is bounded (maxStateFile) and the write was not, so enrolling enough
// operators left a state file every later load refused as corrupt, with every
// grant on the machine behind it.
func TestARosterTooLargeToReadBackIsRefusedBeforeItIsWritten(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	pub, _ := remoteKey(t)
	ops := make([]OperatorKey, 4000)
	for i := range ops {
		ops[i] = OperatorKey{Label: fmt.Sprintf("operator-%d", i), PublicKey: pub}
	}

	verr := EnableRemote(ops, "https://a.example")
	if verr == nil || verr.Code != "core.guard.remote.size" {
		t.Fatalf("EnableRemote over %d operators = %v, want core.guard.remote.size", len(ops), verr)
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Fatal("a guard the next read would refuse as corrupt was left on the machine")
	}

	if verr := EnableRemote(ops[:10], "https://a.example"); verr != nil {
		t.Fatalf("a roster of ten was refused after the large one: %v", verr)
	}
	if _, verr := load(); verr != nil {
		t.Fatalf("what was enrolled does not read back: %v", verr)
	}
}
