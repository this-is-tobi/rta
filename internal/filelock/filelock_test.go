package filelock

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func lockPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "resource.lock")
}

// A lock file left behind by a process that died mid-update must not wedge
// every future caller forever.
func TestAcquireReclaimsAStaleLock(t *testing.T) {
	path := lockPath(t)
	if err := os.WriteFile(path, []byte("1 abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * DefaultStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	release, err := Acquire(path, DefaultStale, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatalf("a stale lock was not reclaimed: %v", err)
	}
	release()
}

// Releasing removes our lock, not whichever lock happens to be at that path.
//
// A holder whose lock was broken as stale has already been replaced. Removing
// by name on the way out deletes the successor's lock and leaves it inside a
// critical section it believes it has to itself.
func TestReleaseDoesNotRemoveSomebodyElsesLock(t *testing.T) {
	path := lockPath(t)
	release, err := Acquire(path, DefaultStale, DefaultRetry, DefaultTimeout)
	if err != nil {
		t.Fatal(err)
	}

	// What a stale break followed by a fresh acquire leaves behind: the same
	// path, somebody else's token.
	successor := []byte("99999 deadbeefdeadbeefdeadbeefdeadbeef\n")
	if err := os.WriteFile(path, successor, 0o600); err != nil {
		t.Fatal(err)
	}

	release()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the successor's lock was deleted by a release that no longer owned it: %v", err)
	}
	if !bytes.Equal(got, successor) {
		t.Errorf("lock file holds %q, want the successor's token", got)
	}
}

// Breaking a stale lock must break the lock it judged, not whatever is at
// that path by the time it swings.
//
// It used to be an unconditional remove, which is wrong twice over: two
// waiters that both find a crashed holder's lock both remove it, the second
// removing the *winner's* fresh lock, and both go on to hold it — two
// callers then run whatever this lock exists to serialize at once.
//
// Deterministic rather than racy on purpose. The first version of this test
// ran eight goroutines at a planted stale lock and passed against the
// unconditional remove every time: the window between one waiter's stat and
// another's remove is a couple of syscalls, so a scheduler that never
// obliges makes the test say "fixed" about code that is not.
func TestBreakingAStaleLockLeavesAFresherOneAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resource.lock")
	if err := os.WriteFile(path, []byte("1 abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	judged, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	// Between the stat that judged it and the break that acts on it, the
	// abandoned lock went away and somebody live took the name: same path,
	// different file.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	successor := []byte("2 live and holding\n")
	if err := os.WriteFile(path, successor, 0o600); err != nil {
		t.Fatal(err)
	}

	breakStale(dir, path, judged)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("a live lock was broken as though it were the stale one: %v", err)
	}
	if !bytes.Equal(got, successor) {
		t.Errorf("lock file holds %q, want the live holder's token", got)
	}
}

// The ordinary case still works: a lock whose holder is gone does get
// reclaimed, or a crash would wedge every caller until somebody deleted a
// file by hand.
func TestBreakingAStaleLockDoesReclaimIt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resource.lock")
	if err := os.WriteFile(path, []byte("1 abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	judged, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	breakStale(dir, path, judged)

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the abandoned lock is still there (err = %v)", err)
	}
	// And nothing was left beside it: a moved-aside lock that stays is a
	// file in the directory that outlives every process that could explain
	// it.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("leftover in the directory: %s", e.Name())
	}
}
