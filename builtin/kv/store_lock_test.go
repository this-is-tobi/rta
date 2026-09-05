package kv

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// kv.Store is the TUI's credential action — "save this into the store so the
// profile can reference it" — and it is a load-modify-save over the whole
// store: decrypt everything, add one entry, re-encrypt everything.
//
// The process running it is also the one serving MCP. So the interleave is
// ordinary rather than exotic: an agent's kv.set holds the store lock while
// this held nothing, the later save wrote a snapshot taken before the earlier
// one landed, and both callers were told they succeeded. This exact
// shape for the capability handlers; this function was written afterwards and
// separately, and did not inherit it.

// storeEnv points kv.Store at the same passphrase the request helper uses.
// Store reads it from the environment (it is called from the TUI, which has
// no plugin.Request to carry one), so without this the two halves of these
// tests would be opening two different stores.
func storeEnv(t *testing.T) {
	t.Helper()
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
}

func TestStoreDoesNotLoseAConcurrentWrite(t *testing.T) {
	storeEnv(t)
	// Seed first, alone, so every writer below races a real load-modify-save
	// against an existing store rather than each creating one from nothing —
	// a different and easier case that would not exercise the bug.
	text(t, runSet, map[string]any{"key": "seed", "value": "v0"}, false)

	const n = 6
	var wg sync.WaitGroup
	errs := make(chan error, n*2)
	for i := range n {
		wg.Add(2)
		// A capability write, which takes the lock.
		go func(i int) {
			defer wg.Done()
			_, err := runSet(context.Background(),
				req(map[string]any{"key": fmt.Sprintf("agent-%d", i), "value": "v"}, false))
			errs <- err
		}(i)
		// The TUI credential action, which is the one that did not.
		go func(i int) {
			defer wg.Done()
			verr := Store(fmt.Sprintf("operator-%d", i), "hunter2", "credential for profile me", "profile:me")
			if verr != nil {
				errs <- verr
				return
			}
			errs <- nil
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a writer failed: %v", err)
		}
	}

	tbl := table(t, runList, nil)
	// n agent entries + n operator entries + the seed. Anything less is a
	// write that reported success and is not there.
	if want := n*2 + 1; len(tbl.Rows) != want {
		t.Fatalf("the store holds %d entries after %d writers, want %d — writes were lost silently",
			len(tbl.Rows), n*2, want)
	}
}

// The control: Store still refuses to overwrite, which is its own rule and
// the reason it is not simply kv.set. Taking the lock must not change that.
func TestStoreStillRefusesToReplaceWhatIsThere(t *testing.T) {
	storeEnv(t)
	if verr := Store("cred", "first", "credential for profile me", "profile:me"); verr != nil {
		t.Fatal(verr)
	}
	verr := Store("cred", "second", "credential for profile me", "profile:me")
	if verr == nil {
		t.Fatal("Store overwrote an existing entry")
	}
	if verr.Code != "kv.exists" {
		t.Fatalf("code = %q, want kv.exists", verr.Code)
	}
	v, err := runGet(context.Background(), req(map[string]any{"key": "cred"}, false))
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(view.Text).Body; got != "first" {
		t.Fatalf("value = %q, want the original", got)
	}
}
