package plugindist

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugintrust"
)

// installHello puts the example plugin in the store through the real path and
// returns what was recorded.
func installHello(t *testing.T) Report {
	t.Helper()
	testData(t)
	repo := gitFixture(t, map[string]string{"hello": helloManifest(t, hello(t), "")})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	r, verr := Install(context.Background(), "hello", io.Discard)
	if verr != nil {
		t.Fatal(verr)
	}
	return r
}

// **The store path states a digest; only the bytes prove one.**
//
// Upgrade reads the installed declaration by launching the installed binary,
// and it used to launch whatever sat at the path the lockfile's digest named —
// no hash, no trust check. That made it the only place in the tree that runs
// plugin code with no admission check, while every other load hashes first and
// refuses what the operator does not trust.
func TestUpgradeRefusesStoreBytesThatAreNotTheLockedDigest(t *testing.T) {
	first := installHello(t)

	// binaryName, not the literal: the store spells it with the platform's
	// suffix, so writing to the bare name left the real artifact untouched and
	// the upgrade correctly succeeded — reported here as "an upgrade ran bytes
	// nobody had approved", which is the alarming way a fixture can lie.
	stored := filepath.Join(StoreDir(), "hello", first.Digest, binaryName("hello"))
	// Not a plugin at all: if this runs, the handshake fails and the test
	// would report plugin.upgrade.old — the point is that it is never asked.
	//
	// Replaced rather than truncated, because installHello just *ran* these
	// bytes. Linux refuses open(O_WRONLY) on an executable image that is still
	// mapped, and it stays mapped past the wait() that reaped the process —
	// measured at ~20ms during which /proc holds no process to blame. So
	// os.WriteFile here failed with ETXTBSY on Linux and never once on macOS,
	// which does not enforce the rule at all. A rename puts a new inode at the
	// path without ever asking for write access on the executed one.
	//
	// And the rename goes through moveExecutable, not a bare os.Rename: on
	// Windows the handle on a just-run image outlives the process and a move
	// issued in that window fails with "access is denied" — seen once in CI
	// here, in the store's own path never, because the store retries
	// (store.go). The test reaches the path exactly the way the code under
	// test does, retry included.
	staged := filepath.Join(filepath.Dir(stored), ".replacement")
	if err := os.WriteFile(staged, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := moveExecutable(staged, stored); err != nil {
		t.Fatal(err)
	}

	_, verr := Upgrade(context.Background(), "hello", io.Discard)
	if verr == nil {
		t.Fatal("an upgrade ran bytes nobody had approved")
	}
	if verr.Code != "plugin.upgrade.drift" {
		t.Fatalf("upgrade = %s (%s), want plugin.upgrade.drift", verr.Code, verr.Message)
	}
	if e, held := LockedFor("hello"); !held || e.Digest != first.Digest {
		t.Errorf("a refused upgrade moved the pin to %v", e)
	}
}

// `rta plugin untrust` promises the artifact will not load again, and an
// upgrade is a load. It used to run the untrusted binary and then silently
// re-trust the new one, so the documented remedy did not hold.
func TestUpgradeWillNotRunAnUntrustedInstalledBinary(t *testing.T) {
	installHello(t)
	if _, verr := plugintrust.Remove("hello"); verr != nil {
		t.Fatal(verr)
	}

	_, verr := Upgrade(context.Background(), "hello", io.Discard)
	if verr == nil {
		t.Fatal("an upgrade ran an artifact the operator had withdrawn")
	}
	if verr.Code != "plugin.upgrade.untrusted" {
		t.Fatalf("upgrade = %s (%s), want plugin.upgrade.untrusted", verr.Code, verr.Message)
	}
}

// **The digest is a path component before it is anything else.**
//
// ReadLock admits any non-empty string, and Upgrade joined it onto the store
// path — so a hand-edited rta.lock made filepath.Join clean the climb and rta
// launched a binary from outside the store entirely. lock.go says of itself
// that "nothing authorizes through this record"; this was the one line that
// made that false, and the shape check has to run before the Join rather than
// after it.
func TestUpgradeRefusesALockfileDigestThatIsAPath(t *testing.T) {
	first := installHello(t)
	orig, _ := LockedFor("hello")

	elsewhere := t.TempDir()
	marker := filepath.Join(elsewhere, "ran")
	planted := filepath.Join(elsewhere, binaryName("hello"))
	if err := os.WriteFile(planted, []byte("#!/bin/sh\ntouch "+marker+"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The climb that filepath.Join resolves into elsewhere, plus the other
	// shapes ReadLock lets through. Written straight into the lockfile, which
	// is what somebody with the data dir has.
	//
	// The empty digest is here to record that it is refused a step earlier:
	// ReadLock drops an entry with no digest at all, so Upgrade never sees the
	// plugin as managed. Different code, same outcome, and worth pinning so a
	// later relaxation of ReadLock does not open this quietly.
	cases := []struct{ digest, want string }{
		{relativeTo(t, filepath.Join(StoreDir(), "hello"), elsewhere), "plugin.upgrade.lock"},
		{"abc", "plugin.upgrade.lock"},
		{"ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789", "plugin.upgrade.lock"},
		{"hello/../../../..", "plugin.upgrade.lock"},
		{"", "plugin.upgrade.unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.digest, func(t *testing.T) {
			// Rebuilt from the install's own entry each time: a digest ReadLock
			// drops leaves no entry for the next case to mutate.
			entry := orig
			entry.Digest = tc.digest
			if verr := recordInstall(entry); verr != nil {
				t.Fatal(verr)
			}
			t.Cleanup(func() { _ = recordInstall(orig) })

			_, verr := Upgrade(context.Background(), "hello", io.Discard)
			if verr == nil {
				t.Fatal("a lockfile digest that is not a digest was accepted")
			}
			if verr.Code != tc.want {
				t.Errorf("upgrade = %s (%s), want %s", verr.Code, verr.Message, tc.want)
			}
			if _, err := os.Stat(marker); err == nil {
				t.Fatal("rta launched a binary from outside the store")
			}
		})
	}

	// And the honest digest still works, so the shape check is not just
	// refusing everything.
	if e, held := LockedFor("hello"); !held || e.Digest != first.Digest {
		t.Fatalf("the lockfile did not survive the mutations: %v", e)
	}
	if _, verr := Upgrade(context.Background(), "hello", io.Discard); verr != nil {
		t.Errorf("an untouched install no longer upgrades: %v", verr)
	}
}

func relativeTo(t *testing.T, from, to string) string {
	t.Helper()
	rel, err := filepath.Rel(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return rel
}
