package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// An index remove detaches the clone whole before any of it goes. It was
// removed where it stood, unheld, so an exit taken while the removal ran left
// a clone with part of its files gone that every command still read as
// attached, since what is attached is what the indexes directory holds. The
// clone is moved out of that directory first, in one rename, and an exit
// taken while what was moved is removed finishes removing it.
func TestAnIndexRemoveDetachesTheCloneBeforeAnyOfItGoes(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatalf("add: %v", verr)
	}

	original := removeAll
	t.Cleanup(func() { removeAll = original })
	var (
		calls          int
		attachedDuring bool
	)
	removeAll = func(path string) error {
		calls++
		if calls > 1 {
			return original(path)
		}
		_, attachedDuring = IndexByName("lab")
		// Part of the clone goes, then the exit lands, as a forced one does
		// (internal/app's signal handling): the removal is not held off it.
		_ = original(filepath.Join(path, "lab", "index"))
		settled := make(chan func(), 1)
		go func() { settled <- shutdown.Settle() }()
		select {
		case resume := <-settled:
			shutdown.Exiting()
			resume()
		case <-time.After(time.Second):
			t.Error("the exit waited for the removal of a clone already detached")
			(<-settled)()
		}
		return original(path)
	}

	if verr := RemoveIndex("lab"); verr != nil {
		t.Fatalf("remove: %v", verr)
	}
	if calls == 0 {
		t.Fatal("the removal never ran")
	}
	if attachedDuring {
		t.Error("the index was still attached while its clone was being removed")
	}
	left, err := os.ReadDir(indexesDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		t.Errorf("the exit left %s in the indexes directory", e.Name())
	}
}
