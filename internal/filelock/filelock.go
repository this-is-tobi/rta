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
	"sync"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/atomicfile"
)

// Defaults match what internal/grant used before this package existed.
//
// Variables rather than constants for one reason, and it is not
// configuration: a race-instrumented build raises the two durations that
// bound waiting (see slow_race.go). Nothing else writes to them, and the
// values below are what every real invocation uses.
var (
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
//
// **`stale` is a lease, not a deadline on the work.** A holder renews its
// sentinel while it holds it, so the threshold measures silence rather than
// elapsed time and a caller doing slow work keeps its lock for as long as the
// work takes. `timeout` is the unrelated question of how long a *waiter* is
// willing to queue: reaching it is an honest failure — somebody else is
// working — where breaking in would be a silently lost write.
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
			beat := renew(path, mine, stale)
			return func() { beat.stop(); releaseLock(path, mine) }, nil
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

// beats is how many times a holder restamps its sentinel within one lease.
// Five: the holder has to miss every one of them before a waiter is entitled
// to conclude it is gone, so an ordinary scheduling hiccup costs a beat rather
// than the lock.
const beats = 5

// renew starts saying, for as long as this call holds the lock, that the
// holder is still alive.
//
// **A lock is a lease, and the timestamp is what renews it.** Publish stamps
// the sentinel once, when it is created; without this, that one stamp is the
// only thing a waiter ever sees, so it reads "created at T" as "last known
// alive at T" and every holder still working after `stale` looks like a
// corpse. Breaking a live holder's lock puts two callers inside the
// read-modify-write this package exists to serialize, and the later save
// writes a snapshot taken before the earlier one landed — both callers told
// they succeeded, one of the writes simply gone. It is not a slow-machine
// curiosity: the store lock is held across a decrypt, an edit and an encrypt,
// and `rta kv` can stop to ask for a passphrase inside it.
//
// Timer rather than a parked goroutine: locks in this codebase are usually
// held for a few milliseconds and released long before the first beat, and
// that case should cost a timer that never fires rather than a goroutine
// spawned and joined every time a key is written.
//
// What it deliberately does not do is prove the holder is alive by any means
// other than the holder saying so. A process stopped hard enough to miss five
// consecutive beats — SIGSTOP, a pathological pause — is indistinguishable
// from one that died, and after the lease it loses the lock. That is the
// bargain every lease makes, and the lease is generous by comparison with the
// work it covers.
func renew(path string, mine []byte, stale time.Duration) *heartbeat {
	every := stale / beats
	if every <= 0 {
		every = time.Millisecond
	}
	h := &heartbeat{path: path, mine: mine, every: every}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timer = time.AfterFunc(every, h.beat)
	return h
}

type heartbeat struct {
	path  string
	mine  []byte
	every time.Duration

	mu      sync.Mutex
	timer   *time.Timer
	stopped bool
}

// beat restamps the sentinel and arms the next one — but only while the
// sentinel is still this call's own lock. A holder that was broken as stale
// has been replaced, and going on stamping the file would hold somebody else's
// lease open on their behalf. Any error is the end of it: the lock reverts to
// being judged by its last stamp, which is exactly the behaviour that existed
// before there were any.
func (h *heartbeat) beat() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return
	}
	if held, err := os.ReadFile(h.path); err != nil || !bytes.Equal(held, h.mine) {
		return
	}
	now := time.Now()
	if err := os.Chtimes(h.path, now, now); err != nil {
		return
	}
	h.timer = time.AfterFunc(h.every, h.beat)
}

// stop ends the renewal, and does not return while a beat is in flight —
// release removes the file immediately afterwards, and a beat still running
// then would be stamping whatever took the name next.
func (h *heartbeat) stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped = true
	if h.timer != nil {
		h.timer.Stop()
	}
}

// breakStale reclaims a lock left behind by a process that crashed holding
// it — a holder still working renews its lease (see renew), so a sentinel
// that has gone quiet for a whole lease is evidence of death rather than of
// slowness — and only that lock.
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
