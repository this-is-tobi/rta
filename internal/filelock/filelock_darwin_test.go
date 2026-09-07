package filelock

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// ufImmutable is BSD's UF_IMMUTABLE (chflags(2)) — not exported by the
// standard syscall package on darwin, so named here from its well-known
// value. Owner-settable with no root, unlike Linux's analogous chattr +i,
// which needs CAP_LINUX_IMMUTABLE — this reproduction is darwin-only for
// that reason, not merely for the syscall's name.
const ufImmutable = 0x2

// E1. A lock confirmed dead — stale, same identity, not renewed since
// judging — that still cannot be removed must be a named error, not silent
// continued spinning: the old code skipped straight past both the deadline
// check and the retry sleep on this exact path, measured still running at
// 19s against a 2s timeout.
//
// chflags uchg on the lock file itself is the reproduction, not a read-only
// directory: removing a file needs write on its directory, and a directory
// that cannot be written to also fails the CreateTemp call breakStale's own
// Link-based identity check makes first — a different, earlier failure that
// never reaches the Remove this test is about. uchg protects the file, not
// the directory, so creating other names there — including that same
// temporary link — still works, and only the operations that target this
// one file, link(2) and unlink(2), come back EPERM.
func TestBreakStaleReportsAnUnremovableLock(t *testing.T) {
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
	if err := syscall.Chflags(path, ufImmutable); err != nil {
		t.Fatal(err)
	}
	// Cleared before t.TempDir()'s own cleanup tries to remove the
	// directory tree — t.Cleanup runs last-registered-first, so this,
	// registered after TempDir's, unwinds ahead of it.
	t.Cleanup(func() { _ = syscall.Chflags(path, 0) })

	verr := breakStale(dir, path, judged)
	if verr == nil {
		t.Fatal("an unremovable lock was reported as broken")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the lock vanished despite Remove being refused: %v", err)
	}
}

// The same case through Acquire: it must return the refusal promptly,
// rather than spinning past both the deadline check and the retry sleep
// the way the bug this pins did.
func TestAcquireRefusesAnUnremovableStaleLockRatherThanSpinning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resource.lock")
	if err := os.WriteFile(path, []byte("1 abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * DefaultStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Chflags(path, ufImmutable); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Chflags(path, 0) })

	start := time.Now()
	_, err := Acquire(path, DefaultStale, DefaultRetry, 30*time.Second)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("Acquire succeeded on a lock it could not actually remove")
	}
	// Generous against a loaded CI box, and nowhere near the 30s timeout or
	// the 19s the bug this pins was measured still spinning past.
	if elapsed > 5*time.Second {
		t.Errorf("Acquire took %s to refuse — it is retrying a condition that cannot change "+
			"instead of failing immediately", elapsed)
	}
}
