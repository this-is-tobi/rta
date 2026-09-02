package consent

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A decision whose request is gone is litter with a sting: nothing ever
// reads it again, and a future Ask reusing its 32-bit id would find an
// answer nobody gave that asking. Scan sweeps it — and Ask walks Scan
// under the queue lock before minting an id, which is what makes the
// reuse meet an empty slot.
func TestScanSweepsOrphanedDecisions(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(Dir(), "aa11bb22.decision.json")
	if err := os.WriteFile(orphan, []byte(`{"id":"aa11bb22"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(orphan); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("orphaned decision still on disk: %v", err)
	}

	// A decision beside its living request is not an orphan: the asker is
	// polling for exactly this file, and the sweep must not race it away.
	parked, err := Ask(Call{Cap: "kv.get", Safety: "read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer parked.Close()
	if err := Decide(parked.Request.ID, true, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := Scan(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(Dir(), parked.Request.ID+".decision.json")); err != nil {
		t.Fatalf("a live request's decision was swept: %v", err)
	}
}
