package filelock

import (
	"errors"
	"os"
	"sync/atomic"
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

// patience is how long a test waits for the holder to renew a lock before it
// says the holder never will. A bound on hanging, not a measure of speed: a
// renewal that has stopped does not arrive however long this waits.
const patience = 20 * time.Second

// awaitRenewed waits until the sentinel carries a stamp later than after and
// no older than within, and returns that stamp and its age; once patience has
// run out it returns whatever the sentinel carries, for the caller to report.
//
// **What these tests ask is whether the holder goes on saying it is alive, not
// whether the machine let it say so by the instant a test looked.** The beats
// are timers in the process under test, and a process the machine has held off
// the CPU for a lease's worth of a test's own sleep — a shared runner's steal
// time, a starved vCPU — has beats owed that run the moment it is back. A stamp
// sampled in that gap is the last one from before it, older than the lease, and
// read as a holder gone quiet: a lock judged abandoned, from a process that
// was never anything but slow. Freezing the test binary for 600 ms, a lease and
// a little, failed all three tests that sampled once. Waited for, the stamp
// lands within a beat of the process coming back; a holder that has stopped
// renewing never produces one.
func awaitRenewed(t *testing.T, path string, after time.Time, within time.Duration) (stamp time.Time, age time.Duration) {
	t.Helper()
	deadline := time.Now().Add(patience)
	for {
		// Not mtime, which ends the test at the first look that fails: a look
		// that fails costs a look here, as a refused beat costs a beat.
		if info, err := os.Stat(path); err == nil {
			stamp, age = info.ModTime(), time.Since(info.ModTime())
			if stamp.After(after) && age <= within {
				return stamp, age
			}
		}
		if time.Now().After(deadline) {
			if stamp.IsZero() {
				t.Fatalf("the lock is gone: %s cannot be read", path)
			}
			return stamp, age
		}
		time.Sleep(5 * time.Millisecond)
	}
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

	last, age := awaitRenewed(t, path, first, testLease)
	if !last.After(first) {
		t.Fatalf("the lock's timestamp never moved while it was held (%v): a waiter reads "+
			"the moment it was created as the moment its holder was last alive", last)
	}
	if age > testLease {
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

	// A waiter judges the holder by the sentinel's age at the moment it looks,
	// so the holder is first given the chance to have said it is alive lately
	// (awaitRenewed has the case). A holder that never renews gets none, and
	// the waiter below breaks its lock, as it would have without the wait.
	awaitRenewed(t, path, time.Time{}, testLease/2)

	// Short: the waiter judges on its first look at the sentinel, and every
	// further millisecond is one in which a stall long enough to age the stamp
	// past the lease would make a live holder look dead — the same stall the
	// wait above exists to wait out.
	second, err := Acquire(path, testLease, DefaultRetry, testLease/10)
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

// A beat that fails costs a beat, not the lease.
//
// beats is five so that "the holder has to miss every one of them before a
// waiter is entitled to conclude it is gone" — but the first stamp to fail
// used to be the last one ever armed, so a single refused beat *was* the
// lease: the sentinel froze at its last stamp, the holder went on working, and
// after `stale` a waiter was entitled to break its lock and run the same
// read-modify-write beside it.
//
// The failure is injected because nothing makes Chtimes fail on demand.
func TestAMissedBeatCostsABeatNotTheLease(t *testing.T) {
	path := lockPath(t)
	original := chtimes
	var refused atomic.Int32
	chtimes = func(name string, atime, mtime time.Time) error {
		if refused.CompareAndSwap(0, 1) {
			return errors.New("interrupted system call")
		}
		return original(name, atime, mtime)
	}
	t.Cleanup(func() { chtimes = original })

	release, err := Acquire(path, testLease, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	created := mtime(t, path)
	// Until a beat after the refused one lands, which the lease is long
	// enough for several of.
	last, _ := awaitRenewed(t, path, created, testLease)

	if refused.Load() == 0 {
		t.Fatal("no beat was refused, so this proves nothing about a missed one")
	}
	if !last.After(created) {
		t.Fatalf("the lock's timestamp never moved after one refused beat (%v): the first "+
			"missed beat ended the lease, and a waiter may now break a live holder's lock", last)
	}
}
