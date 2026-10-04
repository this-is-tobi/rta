package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// slowClone makes git stop once a clone has finished and wait there until
// the test lets it return, so the test can look at the indexes directory in
// the moment between git writing a clone and rta calling it attached. It
// returns a wait for that moment and the release from it.
func slowClone(t *testing.T) (cloned, release func()) {
	t.Helper()
	dir := t.TempDir()
	mark, gate := filepath.Join(dir, "cloned"), filepath.Join(dir, "gate")
	script := filepath.Join(dir, "slow-git")
	body := "#!/bin/sh\ngit \"$@\" || exit $?\nfor a in \"$@\"; do\n" +
		"  if [ \"$a\" = clone ]; then\n" +
		"    : > '" + mark + "'\n" +
		"    while [ ! -e '" + gate + "' ]; do sleep 0.05; done\n" +
		"    break\n  fi\ndone\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	saved := gitBin
	gitBin = script
	release = func() { _ = os.WriteFile(gate, nil, 0o600) }
	t.Cleanup(func() { release(); gitBin = saved })
	cloned = func() {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(mark); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("the clone never finished")
	}
	return cloned, release
}

// An index is attached only once its clone is whole, and a forced exit taken
// before then leaves nothing attached. git cloned straight into
// indexes/<name>, and what is attached is whatever that directory holds, so
// a clone under way was listed as the index; and the failure path's removal
// is a call an exit skips, so a clone cut short there, or by an rta killed
// outright with git still writing, stayed attached, missing whatever the cut
// fell across. The clone is made in a dot-directory Indexes never lists and
// renamed in after it has been checked; an exit removes the dot-directory.
func TestAnIndexIsAttachedOnlyOnceItsCloneIsWhole(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	cloned, release := slowClone(t)

	added := make(chan *view.Error, 1)
	go func() { added <- AddIndex(context.Background(), "lab", repo) }()
	cloned()
	if _, ok := IndexByName("lab"); ok {
		t.Error("the index was listed while it was still being attached")
	}

	// The exit lands now, as a forced one does: a clone is not held off it.
	settled := make(chan func(), 1)
	go func() { settled <- shutdown.Settle() }()
	select {
	case resume := <-settled:
		shutdown.Exiting()
		resume()
	case <-time.After(time.Second):
		t.Error("the exit waited for a clone")
		(<-settled)()
	}
	release()
	select {
	case verr := <-added:
		if verr == nil {
			t.Error("an attach the exit cut short reported success")
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the attach never returned")
	}
	if all := Indexes(); len(all) != 0 {
		t.Errorf("an attach the exit cut short left %v attached", all)
	}
	left, err := os.ReadDir(indexesDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		t.Errorf("the exit left %s in the indexes directory", e.Name())
	}
}

// What an attach leaves in the indexes directory is the clone, and a refused
// one leaves nothing: the dot-directory it was made in goes either way.
func TestAnIndexAttachLeavesTheCloneAndNothingElse(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	names := func() []string {
		t.Helper()
		entries, err := os.ReadDir(indexesDir())
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}

	if verr := AddIndexAt(context.Background(), "pinned", repo, "no-such-ref"); verr == nil {
		t.Fatal("a pin naming nothing was attached")
	}
	if left := names(); len(left) != 0 {
		t.Errorf("a refused attach left %v", left)
	}
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatalf("add: %v", verr)
	}
	if left := names(); len(left) != 1 || left[0] != "lab" {
		t.Errorf("an attach left %v, want the clone alone", left)
	}
	if verr := AddIndex(context.Background(), "lab", repo); verr == nil || verr.Code != "plugin.index.exists" {
		t.Errorf("a second attach under the same name: %v", verr)
	}
}
