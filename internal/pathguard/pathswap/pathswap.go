// Package pathswap races a path against the call that opens it, for the tests
// that prove a handler reads only what its guard judged.
//
// The attack it stands in for needs nothing but write access inside a root:
// the guard judges a path, and between that and the handler's open the caller
// puts a symbolic link to somewhere else in the file's place, or in the place
// of a directory above it. A test that checks a link once, before the call,
// proves the guard judges links; it says nothing about a link that was not
// there yet when the guard looked. So the swap runs for as long as the calls
// do, and a call is counted by which of the two states it met.
package pathswap

import (
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// File keeps putting a symbolic link to outside in the place of path, a
// regular file, and the file back, until the test ends. The file is left in
// place when it does.
func File(t testing.TB, path, outside string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	link, spare := path+".swap-link", path+".swap-file"
	swap(t, func() {
		_ = os.Remove(link)
		if os.Symlink(outside, link) == nil {
			_ = os.Rename(link, path)
		}
		hold()
		if os.WriteFile(spare, body, 0o600) == nil {
			_ = os.Rename(spare, path)
		}
		hold()
	})
}

// Dir keeps putting a symbolic link to outside in the place of dir, a
// directory, and the directory back, until the test ends.
//
// Two renames rather than one: rename(2) will not put a directory over a
// link. The moment between them, with nothing at dir, is a third state a call
// can meet, and one it must survive as well.
func Dir(t testing.TB, dir, outside string) {
	t.Helper()
	aside := dir + ".swap-aside"
	swap(t, func() {
		if os.Rename(dir, aside) != nil {
			return
		}
		if os.Symlink(outside, dir) == nil {
			hold()
			_ = os.Remove(dir)
		}
		_ = os.Rename(aside, dir)
		hold()
	})
}

// hold keeps the state just made for a moment of varying length, so that a
// swap lands at every point of a call rather than the same few — and now and
// then for long enough that a whole call fits in one state, however slow the
// call is running (the race detector's, a loaded machine's).
//
// That long hold follows the slowest call Run has seen, twice over, rather
// than a fixed length: a fixed 5 ms was shorter than one audit_deps call on a
// CI runner (about 23 ms), so no call there ever fit in one state, every one
// met the swap, and the run could not show it had seen both sides.
func hold() {
	d := time.Duration(rand.IntN(100)) * time.Microsecond //nolint:gosec // a test's timing jitter, never a secret
	if rand.IntN(4) == 0 {                                //nolint:gosec // the same
		d = max(5*time.Millisecond, 2*time.Duration(slowestCall.Load()))
	}
	time.Sleep(d)
}

// slowestCall is the longest call Run has timed, in nanoseconds, shared by
// every swap running: a longer hold only makes a swap flip less often, which
// harms no other test's race.
var slowestCall atomic.Int64

// swap runs step until the test ends.
func swap(t testing.TB, step func()) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				step()
			}
		}
	}()
	t.Cleanup(func() {
		close(stop)
		wg.Wait()
	})
}

// Outcome is what one call met: what it answered, or why it did not.
type Outcome struct {
	Out string
	Err error
}

// Run makes call again and again while a swap runs, and fails the test if
// any answer leaked, which is what leaked says of it. before is the answer
// the call gave with nothing swapped.
//
// A call met the swap when it was refused or answered otherwise than before
// — a walk that finds a directory gone or a link in its place leaves it out
// and answers all the same. Run stops after enough calls on each side of
// that, or at a deadline, whichever comes first, and says how many there
// were. A run that met only one side proves nothing about the race, so that
// is a failure too: on a machine too loaded for the swap to land between two
// steps of any call, the harness says so rather than passing.
func Run(t testing.TB, before string, call func() Outcome, leaked func(Outcome) bool) {
	t.Helper()
	const enough, most = 200, 20000
	// Three seconds when calls are quick, and up to ten while one side has
	// not been seen, since the holds lengthen as slow calls are timed.
	start := time.Now()
	deadline, grace := start.Add(3*time.Second), start.Add(10*time.Second)
	var calm, met, leaks int
	for i := 0; i < most; i++ {
		if now := time.Now(); now.After(grace) || (now.After(deadline) && calm > 0 && met > 0) {
			break
		}
		began := time.Now()
		o := call()
		if d := int64(time.Since(began)); d > slowestCall.Load() {
			slowestCall.Store(d)
		}
		if leaked(o) {
			leaks++
			if leaks <= 3 {
				t.Errorf("call %d read what the swap put outside: %q, %v", i, o.Out, o.Err)
			}
		}
		if o.Err == nil && o.Out == before {
			calm++
		} else {
			met++
		}
		if calm >= enough && met >= enough {
			break
		}
	}
	t.Logf("%d answered as before, %d met the swap, %d leaked", calm, met, leaks)
	if leaks > 0 {
		t.Errorf("%d calls read outside the root", leaks)
	}
	if calm == 0 || met == 0 {
		t.Errorf("the swap never landed on both sides of a call (%d answered as before, %d met it)", calm, met)
	}
}

// Outside makes a directory beside root, outside it, holding one file named
// name with body in it, and returns the directory and the file.
func Outside(t testing.TB, name, body string) (dir, file string) {
	t.Helper()
	dir = t.TempDir()
	file = filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, file
}
