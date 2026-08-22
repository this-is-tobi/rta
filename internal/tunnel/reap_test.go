//go:build !windows

package tunnel

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// group starts a process group containing two processes: the leader, and a
// background sleep it spawns. Two, because one cannot tell "signalled the
// leader" apart from "signalled the group".
//
// Returns the leader's command and the background child's pid.
func group(t *testing.T) (*exec.Cmd, int) {
	t.Helper()
	// The leader prints its child's pid, then outlives it, so the test can
	// ask about each separately.
	cmd := exec.Command("sh", "-c", "sleep 30 & echo $!; exec sleep 30")
	harden(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_, _ = cmd.Process.Wait()
	})
	buf := make([]byte, 32)
	n, err := out.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil {
		t.Fatalf("could not read the background child's pid: %v", err)
	}
	return cmd, child
}

// alive reports whether a pid is still a running process. Signal 0 checks for
// existence without delivering anything.
func alive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// leaderGone waits for rta's own child to exit. Signal 0 cannot answer this:
// an unwaited child is a zombie, and a zombie still has a pid that accepts
// signal 0 — which is how the first version of this test reported that a
// process it had just killed was alive.
func leaderGone(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Errorf("the leader (pid %d) survived reap", cmd.Process.Pid)
	}
}

// childGone waits for a process rta did not spawn. init reaps it, so there is
// no zombie and signal 0 is the right question.
func childGone(t *testing.T, pid int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if !alive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the background child (pid %d) survived reap", pid)
}

func TestReapTakesTheWholeGroupItCreated(t *testing.T) {
	cmd, child := group(t)
	reap(cmd)
	leaderGone(t, cmd)
	childGone(t, child)
}

// The branch that matters is the other one. A command rta did not harden has
// no group of its own, so its pid negated names whatever group it shares —
// rta's own, for anything started outside this package, which is the shell
// that launched rta and every job in it. Deciding from SysProcAttr keeps that
// unreachable; asking Getpgid, which is what this package did, does not.
func TestReapDoesNotSignalAGroupItDidNotCreate(t *testing.T) {
	cmd, child := group(t)
	// The same live process group, described by a command that does not
	// claim it — the shape of every process rta did not spawn itself.
	unclaimed := &exec.Cmd{Process: cmd.Process}
	reap(unclaimed)
	leaderGone(t, cmd)
	if !alive(child) {
		t.Error("reap signalled the whole group of a command rta never hardened;\n" +
			"for a process rta did not spawn that group is rta's own, so this is " +
			"the shell that launched rta receiving a SIGTERM")
	}
}
