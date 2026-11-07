package procgroup

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
	Harden(cmd)
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

// leaderGone waits for the test's own child to exit. Signal 0 cannot answer
// this: an unwaited child is a zombie, and a zombie still has a pid that
// accepts signal 0.
func leaderGone(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Errorf("the leader (pid %d) survived the signal", cmd.Process.Pid)
	}
}

// childGone waits for a process the test did not spawn. init reaps it, so
// there is no zombie and signal 0 is the right question.
func childGone(t *testing.T, pid int) {
	t.Helper()
	for range 200 {
		if !alive(pid) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("the background child (pid %d) survived the signal", pid)
}

func TestSignalTakesTheWholeGroupItCreated(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
		cmd, child := group(t)
		Signal(cmd, sig)
		leaderGone(t, cmd)
		childGone(t, child)
	}
}

// The branch that matters is the other one. A command rta did not harden has
// no group of its own, so its pid negated names whatever group it shares —
// rta's own, for anything started elsewhere, which is the shell that launched
// rta and every job in it.
func TestSignalDoesNotReachAGroupItDidNotCreate(t *testing.T) {
	cmd, child := group(t)
	// The same live process group, described by a command that does not
	// claim it — the shape of every process rta did not spawn itself.
	Signal(&exec.Cmd{Process: cmd.Process}, syscall.SIGTERM)
	leaderGone(t, cmd)
	if !alive(child) {
		t.Error("the whole group of a command rta never hardened was signalled; for a process " +
			"rta did not spawn that group is rta's own")
	}
}

// A process already gone, and a command that never started, are answered
// without signalling anything wider: never group 0 or 1, which would be every
// process in rta's own session and everything the init system owns.
func TestSignalIsSafeOnAProcessThatIsGone(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	Harden(cmd)
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	Signal(cmd, syscall.SIGKILL)
	Signal(nil, syscall.SIGKILL)
	Signal(&exec.Cmd{}, syscall.SIGKILL)
}

// The group is the command's own: one shared with the test runner would make
// every Signal one to the runner, its shell and everything else open.
func TestAHardenedCommandGetsItsOwnGroup(t *testing.T) {
	cmd := exec.Command("sleep", "5")
	Harden(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Signal(cmd, syscall.SIGKILL); _, _ = cmd.Process.Wait() })
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if own, err := syscall.Getpgid(os.Getpid()); err == nil && pgid == own {
		t.Errorf("the command shares the runner's process group (%d)", pgid)
	}
}
