//go:build !windows

package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether pid names a live process, using the kernel rather
// than trusting anything the test itself tracked — signal 0 does the
// permission and existence checks without actually sending a signal.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// patience is how long this file's waits give a machine to do what they wait
// for. Every wait here returns the moment its condition holds, so it bounds
// only the failure, and a bound a loaded machine can pass is one that reports
// the machine.
const patience = 60 * time.Second

// The regression procattr_unix.go exists to close: a wedged program that
// had already forked a child of its own — the same shape xclip's own
// successful path takes, backgrounding a helper to keep serving the
// selection — must not leave that child running once Copy gives up on it.
// exec.CommandContext's default Cancel only reaches the direct child, which
// is exactly the gap harden (Setpgid) and reap (kill the group) close.
//
// The script backgrounds a real, independent process, records its pid
// before blocking, and never itself becomes that process — sh does not
// exec-optimize a backgrounded command, so this does not depend on that
// being true the way calling the long-running program directly would.
//
// **Copy is given up on once the script has set itself up, not a fixed time
// after it was started.** Left to its deadline, the program is killed five
// seconds in whatever it had managed by then: a script that a very busy
// machine had not yet got as far as its pid file is killed before it has
// recorded the child this test is about, and the test reports the machine.
// The deadline is moved out of the way and the context copyUnder is given
// ends the program at the moment the test has seen what it needs to — through
// the same Cancel the deadline reaches, so it is the kill under test. A
// stand-in that takes six seconds to start does not fail it.
func TestCopyKillsWhateverTheWedgedProgramForked(t *testing.T) {
	old := timeout
	timeout = time.Hour
	t.Cleanup(func() { timeout = old })

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	name := Commands()[0].Name
	script := "#!/bin/sh\n" +
		"tail -f /dev/null &\n" +
		"echo $! > " + pidFile + "\n" +
		"wait\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, giveUp := context.WithCancel(context.Background())
	defer giveUp()
	done := make(chan struct{})
	go func() {
		copyUnder(ctx, []byte("s3cr3t"))
		close(done)
	}()

	// A pid file that is there but not yet holding its pid is not ready: the
	// shell makes the file before it writes it.
	var pid int
	for deadline := time.Now().Add(patience); pid == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stub never recorded its child's pid")
		}
		if raw, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
		}
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	giveUp()
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("Copy did not return after it was given up on")
	}

	for deadline := time.Now().Add(patience); alive(pid) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("pid %d (the wedged program's own child) is still running after Copy gave up on its parent", pid)
	}
}
