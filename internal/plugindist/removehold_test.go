package plugindist

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/plugintrust"
	"github.com/this-is-tobi/rta/internal/shutdown"
)

// settleDuringRemoval makes every store removal first try to settle the
// process, as a forced exit does, and reports whether one ever settled there:
// with trust already withdrawn and the removal, or the lock entry after it,
// still to come. finish lets the process resume once the command has returned.
func settleDuringRemoval(t *testing.T) (between func() bool, finish func()) {
	t.Helper()
	original := removeAll
	t.Cleanup(func() { removeAll = original })
	settled := make(chan func(), 1)
	var landed, tried bool
	removeAll = func(path string) error {
		if !tried {
			tried = true
			go func() { settled <- shutdown.Settle() }()
			select {
			case resume := <-settled:
				landed = true
				resume()
			case <-time.After(100 * time.Millisecond):
			}
		}
		return original(path)
	}
	return func() bool { return landed }, func() {
		if tried && !landed {
			(<-settled)()
		}
	}
}

// A remove lands whole past a forced exit. Its writes are three — trust
// withdrawn from every stored digest, the store and bin/ link removed, the
// line in rta.lock dropped — and an exit that fell between them left a plugin
// rta.lock still records whose trust was gone, which install then refuses as
// present and upgrade as untrusted, or one with no store that rta.lock still
// names. The exit waits for all three once the first has begun.
func TestAnExitWaitsForARemoveToLandWhole(t *testing.T) {
	testData(t)
	bin := hello(t)
	attach(t, helloManifest(t, bin, ""))
	rep, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatal(verr)
	}

	between, finish := settleDuringRemoval(t)
	if _, verr := Remove("hello"); verr != nil {
		t.Fatalf("remove: %v", verr)
	}
	finish()
	if between() {
		t.Fatal("the process settled with trust withdrawn and the store and lock entry still there")
	}
	if plugintrust.Load().Trusts(rep.Digest) {
		t.Error("the digest is still trusted")
	}
	if _, held := LockedFor("hello"); held {
		t.Error("the exit did not wait for the lock entry")
	}
}

// The same for a prune, one stored version at a time: its trust and its copy
// go together, so an exit leaves no version in the store that nothing trusts
// any more, which a load would then refuse as never approved.
func TestAnExitWaitsForAPrunedVersionToGoWhole(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	old, cur := strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, d := range []string{old, cur} {
		if verr := plugintrust.Add(d, "hello", stored(t, "hello", d, "version "+d[:1])); verr != nil {
			t.Fatal(verr)
		}
	}
	current(t, "hello", cur)

	between, finish := settleDuringRemoval(t)
	if _, verr := Prune(); verr != nil {
		t.Fatalf("prune: %v", verr)
	}
	finish()
	if between() {
		t.Fatal("the process settled with a version's trust withdrawn and its copy still stored")
	}
	if got := StoredDigests("hello"); strings.Join(got, " ") != cur {
		t.Errorf("store holds %v, want the current version alone", got)
	}
}
