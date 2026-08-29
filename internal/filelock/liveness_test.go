package filelock

import (
	"os"
	"testing"
	"time"
)

// A lock is a lease, and a lease has to be renewed.
//
// breakStale's own comment says it "reclaims a lock left behind by a process
// that crashed holding it — and only that lock". The only evidence it consults
// is the sentinel's modification time, which atomicfile.Publish stamps once
// when the lock is created and nothing ever moves again. So the file says
// "created at T", the waiter reads it as "last known alive at T", and after
// `stale` seconds of ordinary work every live holder looks like a corpse.
//
// The consequence is the one this package exists to prevent: the waiter breaks
// the lock, creates its own, and two callers run a load-modify-save over the
// same file at once — the second save writing a snapshot taken before the
// first one landed, with both callers told they succeeded.
//
// It is not a hypothetical delay. The store lock is held across an age decrypt,
// an edit and an encrypt; `rta kv` can prompt for a passphrase inside it; a
// machine under real load stretches all of it. slow_race.go already reasons
// about exactly this shape — it raises DefaultStale alongside DefaultTimeout
// precisely so that instrumented holders are not judged dead — which is the
// same argument applied to a build tag rather than to production.
//
// These use an explicit short lease rather than the package defaults so the
// tests cost a second rather than half a minute; the beat interval is derived
// from the lease, so a 500ms lease is renewed every 100ms.

const testLease = 500 * time.Millisecond

func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the lock is gone: %v", err)
	}
	return info.ModTime()
}

// The invariant, directly: while somebody holds the lock, the sentinel keeps
// saying so.
func TestAHeldLockKeepsSayingItIsAlive(t *testing.T) {
	path := lockPath(t)
	release, err := Acquire(path, testLease, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	first := mtime(t, path)
	// Well past the point at which a waiter would judge this lock abandoned.
	time.Sleep(testLease + testLease/2)

	last := mtime(t, path)
	if !last.After(first) {
		t.Fatalf("the lock's timestamp never moved while it was held (%v): a waiter reads "+
			"the moment it was created as the moment its holder was last alive", last)
	}
	if age := time.Since(last); age > testLease {
		t.Fatalf("a live holder's lock is %v old against a %v lease — it looks abandoned", age, testLease)
	}
}

// And the consequence: a second caller must wait for a live holder and fail
// honestly, never break in beside it.
func TestALiveHoldersLockIsNotBrokenAsStale(t *testing.T) {
	path := lockPath(t)
	release, err := Acquire(path, testLease, DefaultRetry, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	// The holder is still inside its critical section — it is slow, not dead.
	time.Sleep(testLease + testLease/2)

	second, err := Acquire(path, testLease, DefaultRetry, testLease/2)
	if err == nil {
		second()
		t.Fatal("two callers hold the same lock at once: the second judged a live holder " +
			"dead and broke its lock, which is the corruption this package exists to prevent")
	}
}

// The other half stays true: a lock whose holder really is gone is still
// reclaimed, or one crash wedges every future caller until somebody deletes a
// file by hand. The renewal must be tied to a living holder, not to the file.
func TestAnAbandonedLeaseIsStillReclaimed(t *testing.T) {
	path := lockPath(t)
	if err := os.WriteFile(path, []byte("1 abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * testLease)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	release, err := Acquire(path, testLease, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatalf("a genuinely abandoned lock was not reclaimed: %v", err)
	}
	release()
}

// Releasing stops the renewal. A beat that outlived its release would keep
// stamping a sentinel this call no longer owns — and the whole point of the
// timestamp is that it means "the holder is alive", so refreshing one for a
// caller that has left is the same lie in the other direction.
func TestReleasingStopsTheRenewal(t *testing.T) {
	path := lockPath(t)
	release, err := Acquire(path, testLease, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	release()

	// A successor takes the name, as it may the instant the lock is free.
	successor := []byte("99999 deadbeefdeadbeefdeadbeefdeadbeef\n")
	if err := os.WriteFile(path, successor, 0o600); err != nil {
		t.Fatal(err)
	}
	planted := mtime(t, path)
	time.Sleep(testLease)

	if got := mtime(t, path); got.After(planted) {
		t.Fatal("a released holder is still renewing the lock file, which now belongs to somebody else")
	}
}
