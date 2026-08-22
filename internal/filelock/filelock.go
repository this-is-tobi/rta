// Package filelock serializes read-modify-write access to a file-backed
// resource across processes and goroutines: a sentinel file next to the
// resource, published exactly once, held by whichever caller's token ends up
// in it.
//
// Extracted from internal/grant, which built and proved this mechanism first
// for grants.json — two MCP tool calls spending the same one-time grant at
// once, or two `rta mcp serve` processes sharing one data directory, must not
// both see a resource unspent/unedited and both succeed. builtin/kv's store
// has the identical shape: load, mutate, save, with nothing between the load
// and the save stopping a second writer from doing the same and one of them
// silently losing. Rather than growing a second, independently-maintained
// copy of this logic, both now share one.
package filelock

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
)

// Defaults match what internal/grant used before this package existed.
const (
	DefaultStale   = 5 * time.Second
	DefaultRetry   = 10 * time.Millisecond
	DefaultTimeout = 2 * time.Second
)

// Acquire takes the lock at path, creating path's directory if needed.
// path is the sentinel file itself (e.g. filepath.Join(dir, "store.lock")),
// never the resource it protects — Acquire never touches that.
//
// The lock is a sentinel file, not flock(2): creating a name that cannot
// already exist behaves identically on every platform rta ships for (Linux,
// macOS, Windows), where POSIX file locking does not.
//
// **The lock is held by identity, not by name.** Every operation on the
// sentinel is by content — release removes it only if it still holds this
// call's own token, and a waiter reclaiming a stale lock moves it aside and
// confirms by identity that it moved the one it judged before creating its
// own. Two waiters both finding a crashed holder's lock both moving it and
// both creating their own would mean both held it; without the identity
// check, a holder whose lock had been broken as stale would remove its
// successor's on the way out.
func Acquire(path string, stale, retry, timeout time.Duration) (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	mine, err := token()
	if err != nil {
		return nil, fmt.Errorf("acquiring lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		// Publish is create-once and reports the contents that ended up
		// there, which is the whole of an acquire: our token back means we
		// created it, anything else names the holder that beat us.
		held, err := atomicfile.Publish(path, mine, 0o600)
		if err != nil {
			return nil, fmt.Errorf("acquiring lock: %w", err)
		}
		if bytes.Equal(held, mine) {
			return func() { releaseLock(path, mine) }, nil
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > stale {
			breakStale(filepath.Dir(path), path, info)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for another call to finish using %s", path)
		}
		time.Sleep(retry)
	}
}

func token() ([]byte, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf("%d %s\n", os.Getpid(), hex.EncodeToString(nonce))), nil
}

// releaseLock gives the lock up, and only if it is still ours.
//
// A holder whose lock was broken as stale has already been replaced. Removing
// the file by name on the way out would delete its successor's lock and leave
// that successor inside a critical section it believes it has to itself.
func releaseLock(path string, mine []byte) {
	if held, err := os.ReadFile(path); err != nil || !bytes.Equal(held, mine) {
		return
	}
	_ = os.Remove(path)
}

// breakStale reclaims a lock left behind by a process that crashed holding
// it — and only that lock.
//
// Moving it aside first is what makes this safe against a second waiter
// doing the same thing: rename succeeds for exactly one of them, and the
// loser finds nothing to move. The identity check is for the narrower case
// where the file changed between the stat that judged it stale and the
// rename that took it — the file we moved is then somebody's live lock, and
// it goes back. Link rather than Rename to put it back, so restoring can
// never overwrite a lock a third process has since taken.
func breakStale(dir, path string, judged os.FileInfo) {
	tmp, err := os.CreateTemp(dir, ".lock-stale-*")
	if err != nil {
		return
	}
	name := tmp.Name()
	_ = tmp.Close()
	// Whatever happens below, the moved-aside copy does not stay: it is not
	// the lock any more, and a leftover in the data directory outlives every
	// process that could explain it.
	defer os.Remove(name)

	if err := os.Rename(path, name); err != nil {
		return // another waiter moved it first, or the holder released it
	}
	if after, err := os.Stat(name); err == nil && !os.SameFile(judged, after) {
		_ = os.Link(name, path)
	}
}
