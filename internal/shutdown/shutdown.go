// Package shutdown is what a process asked to stop has to let finish before
// it stops, and what a signal means while it runs.
//
// rta gives a command a short grace, once a signal has cancelled its context,
// to return on its own, and then exits without it (internal/app's
// signal handling): a handler that never reads its context — a blame over a
// large history, a passphrase prompt — otherwise kept the process alive after
// SIGTERM for as long as it liked. An exit taken from outside the command can
// land anywhere in it, so the places where landing would do damage say so
// here, and the exit waits for them.
//
// Four kinds of place. Work that leaves something behind if it is cut: a
// store write between its temporary file and its rename (internal/atomicfile),
// or a secret written out in plaintext for an editor and not yet removed
// (builtin/kv's edit). What the command would have undone on its way out, and
// an exit without it has to undo for it: a lock it holds (internal/filelock),
// which kv holds across its passphrase prompt. A terminal lent to a child
// — that editor — whose keystrokes the terminal turns into signals for the
// whole foreground group: emacs's C-g is SIGINT to rta as well, and it means
// the editor is in use, not that rta should stop. And a prompt waiting for
// its answer (internal/stdio's ReadSecret), whose line the exit's report
// would otherwise run on from.
//
// A leaf, so that all of those — under internal, and a built-in — can say so
// without importing the application that acts on it.
package shutdown

import (
	"sync"
	"sync/atomic"
)

var (
	mu   sync.Mutex
	idle = sync.NewCond(&mu)
	held int
	shut bool
)

// Hold marks work in progress that a stopping process has to let finish, and
// returns what marks it done. Cheap enough for every store write: a process
// nobody asks to stop never waits on it.
//
// A Hold taken while the process is settling (Settle) waits until settling
// ends, which for a process about to exit is never: nothing starts that the
// exit would then cut. One taken inside another — the store write that ends
// an edit, inside the Hold on the edit's plaintext — is let through while
// Settle is still waiting for the outer one.
func Hold() (release func()) {
	mu.Lock()
	for shut {
		idle.Wait()
	}
	held++
	mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			held--
			idle.Broadcast()
			mu.Unlock()
		})
	}
}

// Settle waits until nothing is held, and returns with every later Hold kept
// waiting until resume is called. A process calls it on the way to an exit
// it has decided to take, and resumes only if it turns out not to take it —
// the command returned on its own while the writes it was waiting for
// finished.
func Settle() (resume func()) {
	mu.Lock()
	for held > 0 {
		idle.Wait()
	}
	shut = true
	mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			shut = false
			idle.Broadcast()
			mu.Unlock()
		})
	}
}

var (
	hooksMu sync.Mutex
	hooks   = map[int]func(){}
	hookID  int
)

// OnExit registers what an exit taken without the command has to do on its
// way out, that a deferred call in the command would have done: a lock
// sentinel removed, so the next command does not wait out the lease of a
// holder that is gone. It returns what unregisters it, for the command to
// call once it has done the same itself.
func OnExit(fn func()) (done func()) {
	hooksMu.Lock()
	defer hooksMu.Unlock()
	hookID++
	id := hookID
	hooks[id] = fn
	return func() {
		hooksMu.Lock()
		defer hooksMu.Unlock()
		delete(hooks, id)
	}
}

// Exiting runs everything registered with OnExit, for a process that has
// settled (Settle) and is about to exit without the command: nothing held is
// still running then to need what the hooks undo.
func Exiting() {
	hooksMu.Lock()
	pending := make([]func(), 0, len(hooks))
	for id, fn := range hooks {
		pending = append(pending, fn)
		delete(hooks, id)
	}
	hooksMu.Unlock()
	for _, fn := range pending {
		fn()
	}
}

var lent atomic.Int32

// LendTerminal marks the terminal as a child process's for as long as it runs,
// and returns what takes it back. While it is lent, a SIGINT is the child's:
// the terminal sends one to the whole foreground group on the key that means
// interrupt, and an editor that binds that key is being used, not asking rta
// to stop. git ignores SIGINT around its editor for the same reason.
func LendTerminal() (takeBack func()) {
	lent.Add(1)
	var once sync.Once
	return func() { once.Do(func() { lent.Add(-1) }) }
}

// TerminalLent reports whether a child process has the terminal.
func TerminalLent() bool { return lent.Load() > 0 }

var prompts atomic.Int32

// Prompting marks a prompt waiting on the terminal for its answer, and returns
// what marks it answered. A prompt leaves the cursor after its own words, with
// echo off, and the report of an exit taken inside it ran on from them —
// "Passphrase:  ERROR core.signal …" — in the file standard error was sent to
// as much as on a terminal, since a prompt asks whenever standard input is
// one. The report starts a line of its own while one is open.
func Prompting() (answered func()) {
	prompts.Add(1)
	var once sync.Once
	return func() { once.Do(func() { prompts.Add(-1) }) }
}

// PromptOpen reports whether a prompt is waiting for its answer.
func PromptOpen() bool { return prompts.Load() > 0 }
