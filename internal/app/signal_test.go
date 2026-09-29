package app

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/registry"
)

// watch is an Interrupts with its signals, its exit and everything it reads
// in the test's hands. exit ends the goroutine that calls it, as os.Exit ends
// the process, and reports the status.
type watch struct {
	*Interrupts
	ctx      context.Context
	codes    chan int
	restored atomic.Int32
	closed   atomic.Int32
	undone   atomic.Int32
	stderr   *lockedBuffer
}

func newWatch(t *testing.T, grace time.Duration, owned, lent func() bool, settle func() func()) *watch {
	t.Helper()
	w := unattachedWatch(t, grace, owned, lent, settle)
	w.Attach(nil, func() { w.closed.Add(1) })
	return w
}

// unattachedWatch is a watch as main has it while the plugins load, before
// Attach.
func unattachedWatch(t *testing.T, grace time.Duration, owned, lent func() bool, settle func() func()) *watch {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &watch{ctx: ctx, codes: make(chan int, 1), stderr: &lockedBuffer{}}
	if settle == nil {
		settle = func() func() { return func() {} }
	}
	w.Interrupts = &Interrupts{
		signals: make(chan os.Signal, 2),
		cancel:  cancel,
		grace:   grace,
		owned:   owned,
		lent:    lent,
		settle:  settle,
		atExit:  func() { w.undone.Add(1) },
		restore: func() { w.restored.Add(1) },
		exit: func(code int) {
			w.codes <- code
			runtime.Goexit()
		},
		stderr:    w.stderr,
		prompting: never,
		done:      make(chan struct{}),
		attached:  make(chan struct{}),
	}
	go w.run()
	// Not Stop: a forced exit keeps the watch's lock for good, as it may,
	// since the real exit does not return.
	t.Cleanup(cancel)
	return w
}

// lockedBuffer is a stderr the watch writes from its own goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func never() bool { return false }

func (w *watch) exited(within time.Duration) (int, bool) {
	select {
	case code := <-w.codes:
		return code, true
	case <-time.After(within):
		return 0, false
	}
}

// The reason this exists: a handler that never reads its context kept the
// process alive after SIGTERM. The context is cancelled at once, and the
// process exits after the grace with 143, having said why, closed the plugins
// and put the terminal back.
func TestACommandThatIgnoresTheSignalIsExitedAfterTheGrace(t *testing.T) {
	for sig, want := range map[os.Signal]int{syscall.SIGTERM: 143, os.Interrupt: 130} {
		w := newWatch(t, 20*time.Millisecond, never, never, nil)
		w.signals <- sig
		select {
		case <-w.ctx.Done():
		case <-time.After(time.Second):
			t.Fatalf("%v did not cancel the command's context", sig)
		}
		code, ok := w.exited(2 * time.Second)
		if !ok || code != want {
			t.Fatalf("%v: exited %v with %d, want %d", sig, ok, code, want)
		}
		if w.closed.Load() != 1 || w.undone.Load() != 1 || w.restored.Load() != 1 {
			t.Errorf("%v: plugins closed %d times, locks given back %d times, terminal restored %d times, "+
				"want once each", sig, w.closed.Load(), w.undone.Load(), w.restored.Load())
		}
		if got := w.stderr.String(); !strings.Contains(got, "stopped by "+signalName(sig)) ||
			!strings.Contains(got, "had not returned 20ms after being asked to stop") {
			t.Errorf("%v: stderr = %q", sig, got)
		}
	}
}

// A signal while the plugins load cancels the load and waits for it: an exit
// then left every plugin launched to read a declaration running, with nothing
// yet attached to close it. The grace starts once the command is there to be
// given it.
func TestASignalWhileThePluginsLoadWaitsForTheCommand(t *testing.T) {
	w := unattachedWatch(t, 20*time.Millisecond, never, never, nil)
	w.signals <- syscall.SIGTERM
	select {
	case <-w.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("the signal did not cancel the load's context")
	}
	if code, ok := w.exited(200 * time.Millisecond); ok {
		t.Fatalf("exited %d before the command tree and the plugins were attached", code)
	}
	w.Attach(nil, func() { w.closed.Add(1) })
	if code, ok := w.exited(2 * time.Second); !ok || code != 143 {
		t.Fatalf("after Attach: exited %v with %d, want 143 once the grace ran out", ok, code)
	}
	if w.closed.Load() != 1 {
		t.Errorf("the plugins were closed %d times, want once", w.closed.Load())
	}
}

// A command that returns inside the grace exits with its own status: the
// watch stops, and nothing it would have done happens.
func TestACommandThatReturnsInTimeKeepsItsOwnStatus(t *testing.T) {
	w := newWatch(t, 100*time.Millisecond, never, never, nil)
	w.signals <- os.Interrupt
	<-w.ctx.Done()
	w.Stop()
	if code, ok := w.exited(300 * time.Millisecond); ok {
		t.Fatalf("exited %d after the command returned on its own", code)
	}
	if w.closed.Load() != 0 || w.stderr.String() != "" {
		t.Errorf("a command that returned was reported as stopped: %q", w.stderr.String())
	}
}

// A second signal is somebody insisting: it exits at once, in the status of
// the signal that did it, with the terminal put back and nothing waited for.
func TestASecondSignalExitsAtOnce(t *testing.T) {
	w := newWatch(t, time.Hour, never, never, nil)
	w.signals <- syscall.SIGTERM
	<-w.ctx.Done()
	w.signals <- os.Interrupt
	code, ok := w.exited(time.Second)
	if !ok || code != 130 {
		t.Fatalf("exited %v with %d, want 130 at once", ok, code)
	}
	if w.restored.Load() != 1 {
		t.Error("the terminal was not put back")
	}
}

// mcp serve and the TUI stop on their own terms: no grace applies, so their
// shutdown is what it was, and only a second signal exits without them.
func TestACommandThatOwnsItsShutdownGetsNoDeadline(t *testing.T) {
	w := newWatch(t, 10*time.Millisecond, func() bool { return true }, never, nil)
	w.signals <- syscall.SIGTERM
	<-w.ctx.Done()
	if code, ok := w.exited(200 * time.Millisecond); ok {
		t.Fatalf("exited %d under a command that owns its shutdown", code)
	}
	w.signals <- syscall.SIGTERM
	if code, ok := w.exited(time.Second); !ok || code != 143 {
		t.Fatalf("a second signal: exited %v with %d, want 143", ok, code)
	}
}

// While an editor has the terminal, SIGINT is the editor's: emacs's C-g sends
// one to the whole foreground group. SIGTERM is still rta's.
func TestASIGINTWhileTheTerminalIsLentIsTheChilds(t *testing.T) {
	w := newWatch(t, time.Hour, never, func() bool { return true }, nil)
	w.signals <- os.Interrupt
	w.signals <- os.Interrupt
	select {
	case <-w.ctx.Done():
		t.Fatal("a SIGINT to the editor cancelled rta's command")
	case <-time.After(100 * time.Millisecond):
	}
	if code, ok := w.exited(10 * time.Millisecond); ok {
		t.Fatalf("exited %d on the editor's SIGINT", code)
	}
	w.signals <- syscall.SIGTERM
	select {
	case <-w.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("SIGTERM did not cancel the command while an editor ran")
	}
}

// The exit waits for held work — a store write, an edit's plaintext — and a
// second signal still cuts the wait short.
func TestTheExitWaitsForHeldWork(t *testing.T) {
	release := make(chan struct{})
	var resumed atomic.Int32
	settle := func() func() {
		<-release
		return func() { resumed.Add(1) }
	}
	w := newWatch(t, 10*time.Millisecond, never, never, settle)
	w.signals <- syscall.SIGTERM
	if code, ok := w.exited(200 * time.Millisecond); ok {
		t.Fatalf("exited %d with work still held", code)
	}
	close(release)
	if code, ok := w.exited(time.Second); !ok || code != 143 {
		t.Fatalf("exited %v with %d once the work was done, want 143", ok, code)
	}

	blocked := func() func() { select {} }
	w = newWatch(t, 10*time.Millisecond, never, never, blocked)
	w.signals <- syscall.SIGTERM
	if code, ok := w.exited(100 * time.Millisecond); ok {
		t.Fatalf("exited %d with work still held", code)
	}
	w.signals <- syscall.SIGTERM
	if code, ok := w.exited(time.Second); !ok || code != 143 {
		t.Fatalf("a second signal while waiting: exited %v with %d, want 143", ok, code)
	}

	// A command returning while the work settles is not exited under: the
	// settling is undone and the command's own status stands.
	release = make(chan struct{})
	w = newWatch(t, 10*time.Millisecond, never, never, settle)
	w.signals <- syscall.SIGTERM
	time.Sleep(50 * time.Millisecond)
	stopped := make(chan struct{})
	go func() { w.Stop(); close(stopped) }()
	<-stopped
	close(release)
	if code, ok := w.exited(200 * time.Millisecond); ok {
		t.Fatalf("exited %d after the command returned", code)
	}
	deadline := time.Now().Add(time.Second)
	for resumed.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if resumed.Load() != 1 {
		t.Errorf("settling was resumed %d times, want once, for the command that returned", resumed.Load())
	}
}

// On a terminal the report starts a line of its own: it ran on from the
// "Passphrase: " of a prompt still waiting, or from an echoed ^C. So it does
// wherever a prompt is still waiting, since a prompt asks whenever standard
// input is a terminal and writes its words to standard error wherever that
// goes. Anywhere else nothing is added before it.
func TestAForcedExitOnATerminalIsReportedOnALineOfItsOwn(t *testing.T) {
	for _, tc := range []struct{ tty, prompting bool }{{true, false}, {false, true}, {true, true}, {false, false}} {
		w := unattachedWatch(t, 10*time.Millisecond, never, never, nil)
		w.onTTY = tc.tty
		w.prompting = func() bool { return tc.prompting }
		w.Attach(nil, nil)
		w.signals <- syscall.SIGTERM
		if _, ok := w.exited(time.Second); !ok {
			t.Fatal("no exit")
		}
		fresh := tc.tty || tc.prompting
		if got := w.stderr.String(); strings.HasPrefix(got, "\n") != fresh || strings.HasPrefix(got, "\n\n") ||
			!strings.Contains(got, "stopped by SIGTERM") {
			t.Errorf("stderr a terminal %v, a prompt open %v: %q", tc.tty, tc.prompting, got)
		}
	}
}

// The report is the command line's error, in the format it asked for.
func TestAForcedExitIsReportedInTheFormatAskedFor(t *testing.T) {
	root := NewRoot(registry.New(), "test")
	if err := root.PersistentFlags().Set("output", "json"); err != nil {
		t.Fatal(err)
	}
	w := newWatch(t, 10*time.Millisecond, never, never, nil)
	w.Attach(root, nil)
	w.signals <- syscall.SIGTERM
	if _, ok := w.exited(time.Second); !ok {
		t.Fatal("no exit")
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(w.stderr.String()), &env); err != nil {
		t.Fatalf("stderr is not json: %v\n%s", err, w.stderr.String())
	}
	if env["code"] != CodeSignal {
		t.Errorf("code = %v, want %s", env["code"], CodeSignal)
	}
}

// Under plugin dev the report is the command plugin dev ran after `--`: named
// as that command, and drawn in the format its own flags asked for, which only
// the root it ran in parsed. Reported for main's root, it named `rta plugin
// dev` and was drawn pretty for a command that had asked for json.
func TestAForcedExitUnderPluginDevIsReportedForTheCommandItRan(t *testing.T) {
	outer := NewRoot(registry.New(), "test")
	nested := NewRoot(registry.New(), "test")
	w := unattachedWatch(t, 10*time.Millisecond, never, never, nil)
	var line atomic.Pointer[commandLine]
	w.nested = line.Load
	w.Attach(outer, nil)
	line.Store(&commandLine{root: nested, args: []string{"doctor", "-o", "json"}})
	w.signals <- syscall.SIGTERM
	if _, ok := w.exited(time.Second); !ok {
		t.Fatal("no exit")
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(w.stderr.String()), &env); err != nil {
		t.Fatalf("stderr is not json: %v\n%s", err, w.stderr.String())
	}
	if msg, _ := env["message"].(string); env["code"] != CodeSignal || !strings.Contains(msg, "`rta doctor`") {
		t.Errorf("report = %v, want %s naming `rta doctor`", env, CodeSignal)
	}
}

// plugin dev marks the command line it runs for as long as it runs it.
func TestRunningNestedMarksTheCommandLineUntilItReturns(t *testing.T) {
	root := NewRoot(registry.New(), "test")
	done := runningNested(root, []string{"doctor"})
	if got := nestedLine.Load(); got == nil || got.root != root || len(got.args) != 1 {
		t.Errorf("while it runs, the nested line is %v", got)
	}
	done()
	if got := nestedLine.Load(); got != nil {
		t.Errorf("once it has returned, the nested line is %v", got)
	}
}
