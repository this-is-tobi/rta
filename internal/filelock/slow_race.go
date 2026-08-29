//go:build race

package filelock

import "time"

// Under the race detector, holding this lock takes longer than it ever will
// in production, so waiting for it has to allow longer too.
//
// The defaults bound a real machine doing a real load-modify-save: for
// builtin/kv that is an age decrypt, an edit and an encrypt, which is fast
// and is not free. A race-instrumented build makes all of it several times
// slower, and eight concurrent writers serialize behind each other — so the
// last one in the queue waits for eight instrumented encrypt cycles against a
// two-second bound sized for uninstrumented ones. It fails as a lock timeout,
// which reads as a deadlock rather than as arithmetic.
//
// **Both durations move, and the pair is the point.** Raising only the
// timeout would leave a five-second staleness threshold in front of holders
// that now legitimately need longer than five seconds — so a waiter would
// judge a live holder dead, break its lock, and put two writers inside the
// read-modify-write this exists to serialize. That is a corruption bug
// invented by a test-only change, which is worse than the flake. The
// threshold stays comfortably above the wait.
//
// A holder now renews its lease while it works, so that argument no longer
// rests on this file alone — the threshold measures silence rather than
// elapsed work, under -race as anywhere else. The raise stays because an
// instrumented machine under load can miss beats the way it misses deadlines,
// and because a wait that outlives the lease is a shape worth keeping out of
// the suite entirely.
//
// Nothing rta ships is built with -race, so none of this reaches a user.
// TestAcquireReclaimsAStaleLock plants its lock at a multiple of
// DefaultStale rather than at a literal age, so it moves with this.
func init() {
	DefaultStale = 30 * time.Second
	DefaultTimeout = 20 * time.Second
}
