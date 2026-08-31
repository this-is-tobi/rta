//go:build !windows

package clipboard

import (
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
func TestCopyKillsWhateverTheWedgedProgramForked(t *testing.T) {
	// The real default, deliberately not shrunk: unlike the "does not hang"
	// test, this one is not measuring how fast Copy returns, and shrinking
	// the deadline only narrows the window the script's own setup (fork,
	// then write a pid file) has to complete before it can be killed
	// prematurely — which is exactly the race an earlier version of this
	// test lost under `make ci`'s coverage pass on a loaded machine.

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

	done := make(chan struct{})
	go func() {
		Copy([]byte("s3cr3t"))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Copy did not return within 30s")
	}

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the stub never recorded its child's pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("pid file held %q, not a pid: %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(2 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if alive(pid) {
		t.Errorf("pid %d (the wedged program's own child) is still running after Copy gave up on its parent", pid)
	}
}
