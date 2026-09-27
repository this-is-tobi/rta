package shutdown

import (
	"testing"
	"time"
)

// returns reports whether fn returns within a short wait, running it on its
// own goroutine so a test can go on while it blocks.
func returns(fn func()) <-chan struct{} {
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	return done
}

func within(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

// Settle waits for every Hold, a nested one taken while it waits among them,
// and keeps a new one waiting until resumed.
func TestSettleWaitsForEveryHoldAndKeepsNewOnesOut(t *testing.T) {
	outer := Hold()
	var resume func()
	settled := returns(func() { resume = Settle() })
	if within(settled) {
		t.Fatal("Settle returned with a Hold still out")
	}
	// Inside the outer one: let through, or the outer one could never end.
	inner := Hold()
	inner()
	outer()
	if !within(settled) {
		t.Fatal("Settle did not return once nothing was held")
	}
	late := returns(func() { Hold()() })
	if within(late) {
		t.Fatal("a Hold began while the process was settled")
	}
	resume()
	if !within(late) {
		t.Fatal("resume did not let the waiting Hold through")
	}
	// Releasing twice is releasing once.
	release := Hold()
	release()
	release()
	resume = Settle()
	resume()
}

// An exit runs what is still registered, once, and nothing a command already
// did for itself.
func TestExitingRunsWhatIsStillRegistered(t *testing.T) {
	var ran []string
	done := OnExit(func() { ran = append(ran, "done by the command") })
	OnExit(func() { ran = append(ran, "left to the exit") })
	done()
	Exiting()
	Exiting()
	if len(ran) != 1 || ran[0] != "left to the exit" {
		t.Errorf("ran %v, want the one hook left, once", ran)
	}
}

func TestLendingTheTerminalIsCountedAndTakenBack(t *testing.T) {
	if TerminalLent() {
		t.Fatal("lent before anything lent it")
	}
	first, second := LendTerminal(), LendTerminal()
	first()
	first()
	if !TerminalLent() {
		t.Error("taken back while a second child still has it")
	}
	second()
	if TerminalLent() {
		t.Error("still lent after every child gave it back")
	}
}
