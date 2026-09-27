package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/this-is-tobi/rta/internal/shutdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// What a signal does to a running command.
//
// SIGINT and SIGTERM cancel the command's context, which is all main did with
// them, through signal.NotifyContext — and catching a signal is what takes its
// default action away. A handler that never reads its context kept the
// process alive for as long as it liked after being asked to stop: `timeout
// 120 rta git blame . big.txt` on a large history ran on past its two
// minutes, and so did a passphrase prompt somebody pressed ^C at. A command
// cannot be relied on to listen, so the command line does: once a signal has
// cancelled the context, the command has signalGrace to return on its own, and
// then rta exits without it, with the status a shell expects of a process a
// signal stopped — 130 for SIGINT, 143 for SIGTERM — and a coded error in the
// format asked for. A second signal exits at once.
//
// Three things are not cut by that exit. A command that stops on its own
// terms — `mcp serve`, which drains its connections, and the TUI, which hands
// the terminal back — owns its shutdown (ownShutdown) and gets no deadline, so
// neither changes. Work internal/shutdown holds — a store write between its
// temporary file and its rename, an editor's plaintext not yet removed — is
// let finish first, and what the command would have undone on its way out —
// a lock it held — is undone for it. And the terminal is put back in the mode
// rta found it in, since the exit can land inside a prompt that turned echo
// off.

// signalGrace is how long a command has, after a signal cancelled its
// context, to return on its own. Longer than the slowest cleanup a command
// does when it does listen — a kube forward's kubectl gets SIGTERM and two
// seconds to go (internal/tunnel's waitDelay) — and short enough that the
// person who pressed ^C is not left wondering whether it registered.
const signalGrace = 3 * time.Second

// CodeSignal is the error a command stopped by a signal ends with, when rta
// had to exit without it: exit status 130 or 143 rather than 1, since the
// command did not fail, it was stopped.
const CodeSignal = "core.signal"

// shutdownOwned is set by a command that stops on its own terms when its
// context is cancelled, however long that takes.
var shutdownOwned atomic.Bool

// ownShutdown marks the running command as one whose shutdown is its own:
// no grace applies to it, and only a second signal exits without it. For
// `mcp serve`, whose drain is bounded by its own grace and whose clean stop
// exits 0, and the TUI, which restores the terminal on the way out.
func ownShutdown() { shutdownOwned.Store(true) }

// Interrupts watches for SIGINT and SIGTERM on behalf of main.
type Interrupts struct {
	signals chan os.Signal
	cancel  context.CancelFunc
	grace   time.Duration
	owned   func() bool
	lent    func() bool
	settle  func() (resume func())
	atExit  func()
	restore func()
	exit    func(code int)
	stderr  io.Writer
	// onTTY is whether stderr is a terminal, and prompting whether a prompt
	// is waiting for its answer: either way the report starts on a line of
	// its own (report).
	onTTY     bool
	prompting func() bool

	mu       sync.Mutex
	root     *cobra.Command
	closeAll func()
	stopped  bool
	done     chan struct{}
	stop     sync.Once
	attached chan struct{}
	attach   sync.Once
}

// WatchSignals returns parent's context cancelled by the first SIGINT or
// SIGTERM, and the watch that exits the process when the command does not
// return within signalGrace of it. main calls Attach once the command tree and
// its plugins exist, and Stop once the command has returned.
func WatchSignals(parent context.Context) (context.Context, *Interrupts) {
	ctx, cancel := context.WithCancel(parent)
	saved := savedTerminal()
	i := &Interrupts{
		signals:   make(chan os.Signal, 2),
		cancel:    cancel,
		grace:     signalGrace,
		owned:     shutdownOwned.Load,
		lent:      shutdown.TerminalLent,
		settle:    shutdown.Settle,
		atExit:    shutdown.Exiting,
		restore:   saved.restore,
		exit:      os.Exit,
		stderr:    os.Stderr,
		onTTY:     stderrIsTerminal(),
		prompting: shutdown.PromptOpen,
		done:      make(chan struct{}),
		attached:  make(chan struct{}),
	}
	signal.Notify(i.signals, os.Interrupt, syscall.SIGTERM)
	go i.run()
	return ctx, i
}

// Attach gives a forced exit what it reports with and what it closes: the
// command tree, whose flags say which format the error is written in, and the
// plugin processes, which live in process groups of their own and outlive rta
// unless something ends them.
//
// The grace runs from here, never from before: a signal that arrives while
// the plugins load cancels the load's context and waits for it. Loading is
// bounded by its own timeouts, and an exit in the middle of it left behind
// every plugin it had launched to read a declaration, and the one still
// starting, since nothing yet held them to close.
func (i *Interrupts) Attach(root *cobra.Command, closeAll func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.root, i.closeAll = root, closeAll
	i.attach.Do(func() { close(i.attached) })
}

// Stop ends the watch once the command has returned: its grace no longer
// applies, and a signal after this has its default action back. A forced
// exit already under way is let finish, so Stop does not return then.
func (i *Interrupts) Stop() {
	i.stop.Do(func() {
		i.mu.Lock()
		i.stopped = true
		i.mu.Unlock()
		signal.Stop(i.signals)
		close(i.done)
	})
}

func (i *Interrupts) run() {
	var (
		first    os.Signal
		expired  <-chan time.Time
		settled  chan func()
		exited   chan struct{}
		attached = i.attached
	)
	// graceFrom starts the command's grace, once there is a command to give
	// it to (Attach) and one that has not taken its shutdown on itself.
	graceFrom := func() {
		if first != nil && attached == nil && !i.owned() {
			expired = time.After(i.grace)
		}
	}
	for {
		select {
		case <-attached:
			attached = nil
			graceFrom()
		case <-i.done:
			// Settling when the command returned: let later writes through,
			// since the process is not exiting on this path after all.
			if settled != nil {
				go func(c chan func()) { (<-c)() }(settled)
			}
			return
		case sig := <-i.signals:
			if sig == os.Interrupt && i.lent() {
				continue
			}
			if first != nil {
				i.restore()
				i.exit(exitStatus(sig))
				return
			}
			first = sig
			i.cancel()
			graceFrom()
		case <-expired:
			expired = nil
			// A command can come to own its shutdown after the signal: one
			// that arrived before `mcp serve` had begun to run.
			if i.owned() {
				continue
			}
			settled = make(chan func(), 1)
			go func(c chan func()) { c <- i.settle() }(settled)
		case resume := <-settled:
			settled = nil
			i.mu.Lock()
			if i.stopped {
				i.mu.Unlock()
				resume()
				return
			}
			// Held from here to the exit, so a command returning now waits in
			// Stop rather than exiting 0 underneath a report that it did not.
			root, closeAll := i.root, i.closeAll
			exited = make(chan struct{})
			go func() {
				defer close(exited)
				i.report(root, first)
				if closeAll != nil {
					closeAll()
				}
				i.atExit()
			}()
		case <-exited:
			i.restore()
			i.exit(exitStatus(first))
			return
		}
	}
}

// report writes the error a forced exit ends with, in the format the command
// line asked for when there is a command line to ask.
func (i *Interrupts) report(root *cobra.Command, sig os.Signal) {
	what := "the command"
	if root != nil {
		if cmd, _, err := root.Find(os.Args[1:]); err == nil && cmd != nil {
			what = "`" + cmd.CommandPath() + "`"
		}
	}
	verr := &view.Error{Code: CodeSignal,
		Message: fmt.Sprintf("stopped by %s: %s had not returned %s after being asked to stop",
			signalName(sig), what, i.grace)}
	// The exit lands wherever the cursor is, and on a terminal that is rarely
	// the start of a line: after the ^C the terminal echoed, or after a
	// "Passphrase: " whose prompt is still waiting with echo off, which the
	// report then ran on from. A prompt asks whenever standard input is a
	// terminal, wherever standard error goes, so an open one ends its line in
	// a file too: `rta kv get x 2>err.log` wrote "Passphrase:  ERROR …".
	if i.onTTY || i.prompting() {
		fmt.Fprintln(i.stderr)
	}
	if root == nil || !RenderTopLevelError(i.stderr, root, verr) {
		fmt.Fprintln(i.stderr, "rta:", verr.Message)
	}
}

// exitStatus is the status a shell reports for a process sig stopped: 128
// and the signal's number.
func exitStatus(sig os.Signal) int {
	if n, ok := sig.(syscall.Signal); ok {
		return 128 + int(n)
	}
	return 128 + int(syscall.SIGTERM)
}

func signalName(sig os.Signal) string {
	switch sig {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return sig.String()
}

// terminal is the mode standard input's terminal was in when rta started, to
// put back on an exit that did not wait for whatever changed it: a passphrase
// prompt turns echo off and turns it back on when it returns, and an exit
// inside it left the shell after it typing blind.
type terminal struct {
	fd    int
	state *term.State
}

// savedTerminal reads fd 0 itself rather than stdio.Real().Fd(): Fd puts a
// descriptor the runtime polls back into blocking mode, and fd 0 is `mcp
// serve`'s request stream as well as the keyboard.
func savedTerminal() *terminal {
	fd := int(syscall.Stdin) //nolint:unconvert // an int here, a Handle on Windows
	if !term.IsTerminal(fd) {
		return nil
	}
	state, err := term.GetState(fd)
	if err != nil {
		return nil
	}
	return &terminal{fd: fd, state: state}
}

// restore puts the terminal back, only while rta is in its foreground: a
// process in the background that sets a terminal's mode is stopped by
// SIGTTOU rather than exiting, and the mode it would set is no longer its own
// to decide — the foreground job's is.
func (t *terminal) restore() {
	if t == nil || !foreground(t.fd) {
		return
	}
	_ = term.Restore(t.fd, t.state)
}
