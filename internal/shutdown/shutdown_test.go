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

func TestAnOpenPromptIsCountedAndAnswered(t *testing.T) {
	if PromptOpen() {
		t.Fatal("open before anything asked")
	}
	first, second := Prompting(), Prompting()
	first()
	first()
	if !PromptOpen() {
		t.Error("answered while a second prompt still waits")
	}
	second()
	if PromptOpen() {
		t.Error("still open after every prompt was answered")
	}
}

// A prompt is open while it waits, and only one that cannot hear its command's
// context being cancelled is a deaf one: the interrupt has no answer to wait for
// from that kind (internal/app's Interrupts).
func TestAPromptIsDeafUnlessItHearsTheContext(t *testing.T) {
	if PromptOpen() || DeafPromptOpen() {
		t.Fatal("a prompt open before any was asked")
	}
	deaf := Prompting()
	if !PromptOpen() || !DeafPromptOpen() {
		t.Error("a prompt that cannot hear the context is not deaf")
	}
	deaf()
	deaf()
	if PromptOpen() || DeafPromptOpen() {
		t.Error("answered twice is answered once, and the count went below zero")
	}
	hearing := PromptingWithContext()
	if !PromptOpen() || DeafPromptOpen() {
		t.Errorf("open %v, deaf %v: a prompt that hears the context is open and not deaf", PromptOpen(), DeafPromptOpen())
	}
	both := Prompting()
	if !DeafPromptOpen() {
		t.Error("a deaf prompt beside a hearing one is hidden by it")
	}
	both()
	hearing()
	if PromptOpen() {
		t.Error("a prompt is open after all were answered")
	}
}
