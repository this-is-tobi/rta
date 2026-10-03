//go:build !windows

package pluginhost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/shutdown"
)

// scriptRunning is a plugin-shaped file that hands what it is given to a
// system program: a script, which only a platform with shebangs can run.
func scriptRunning(t *testing.T, program string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), BinaryName("rta-plugin-script"))
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec "+program+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// go-plugin kills the process it started and nothing else. A plugin that
// shells out — which is most of the interesting ones, and the entire exec
// tier — leaves its children running when it dies: still holding sockets,
// still holding the terminal, invisible to anything rta reports.
//
// This is the test that distinguishes reap(-pgid) from Kill(pid). Both make
// the plugin itself exit, so a test that only checked the plugin would pass
// against the broken version.
func TestReapTakesTheWholeProcessTree(t *testing.T) {
	id, err := Identify(scriptRunning(t, "/bin/sh"))
	if err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	deny, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}

	// A grandchild that outlives its parent, and the parent prints its pid
	// and exits — so by the time we read it, the only thing keeping the
	// grandchild reachable is the process group.
	//
	// The grandchild's stdout goes to /dev/null deliberately. Inherited, it
	// holds the pipe cmd.Output() is reading, so Output blocks for the full
	// sleep even though the child it started has long exited — which is the
	// same reason a real plugin's orphan can wedge a host that waits on
	// output rather than on the process.
	cmd := stagedCmd(t, id, deny, []string{"-c", "sleep 60 >/dev/null 2>&1 & echo $!; exit 0"})
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("spawning: %v", err)
	}
	grandchild, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("could not read the grandchild pid from %q: %v", out, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(grandchild, syscall.SIGKILL) })

	if !alive(grandchild) {
		t.Fatal("the grandchild was already gone, so this test proves nothing")
	}
	reap(cmd)

	deadline := time.Now().Add(5 * time.Second)
	for alive(grandchild) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d survived reap, so a plugin's children outlive it", grandchild)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The plugin has to land in its own process group, or reap's negative pid
// signals the group rta itself is in — which is the test runner, the shell,
// and everything else the user has open.
func TestAPluginGetsItsOwnProcessGroup(t *testing.T) {
	id, err := Identify("/bin/sh")
	if err != nil {
		t.Skipf("no /bin/sh: %v", err)
	}
	deny, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	cmd := stagedCmd(t, id, deny, []string{"-c", "sleep 5"})
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("Setpgid was not requested, so reap would signal rta's own group")
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reap(cmd) })

	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if own, err := syscall.Getpgid(os.Getpid()); err == nil && pgid == own {
		t.Errorf("the plugin shares rta's process group (%d): a reap would signal the test runner", pgid)
	}
}

// A failed Getpgid must degrade to killing the one process, never to
// signalling group 0 or 1 — which would be "every process in rta's own
// session" and "everything the init system owns".
func TestReapIsSafeOnAProcessThatIsGone(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	// Must not panic and must not signal anything wider.
	reap(cmd)
	reap(nil)
	reap(&exec.Cmd{})
}

func alive(pid int) bool {
	// Signal 0 tests for existence without delivering anything.
	return syscall.Kill(pid, 0) == nil
}

// A launch under way holds off a forced exit until it ends, and the exit then
// closes what it produced. A launch runs outside the host's lock and waits out
// the handshake whatever its context says, so the process it is starting is in
// no map CloseAll reads until it ends: an exit that closed the host meanwhile
// closed everything but that one, which then outlived rta in a process group
// of its own.
func TestALaunchUnderWayHoldsOffAForcedExit(t *testing.T) {
	giveUpSoon(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	p := filepath.Join(dir, "rta-plugin-silent")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho $$ > "+pidFile+"\nexec sleep 60\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	opened := make(chan error, 1)
	go func() {
		_, err := h.Open(context.Background(), p)
		opened <- err
	}()
	pid := pidWritten(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	// The exit's order: settle, close the host, run the hooks.
	resume := shutdown.Settle()
	h.CloseAll()
	shutdown.Exiting()
	// A killed process stays a zombie until the wait go-plugin runs on it
	// collects it, and signal 0 reaches a zombie, so give that a moment.
	for deadline := time.Now().Add(2 * time.Second); alive(pid); {
		if time.Now().After(deadline) {
			t.Errorf("pid %d, still starting when the exit began, was running once it had closed the host", pid)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	resume()
	if err := <-opened; err == nil {
		t.Error("a plugin that never answered was opened")
	}
}

// Nor does a launch start once a forced exit has begun: the command it is for
// runs on after the exit has closed the host, and a process it started then
// was on no list the exit reads. Its Hold waits instead, for good in a process
// about to exit, and here until the settle is let go.
func TestNoLaunchStartsOnceAForcedExitHasBegun(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	p := filepath.Join(dir, "rta-plugin-late")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := New()
	t.Cleanup(h.CloseAll)
	resume := shutdown.Settle()
	opened := make(chan error, 1)
	go func() {
		_, err := h.Open(context.Background(), p)
		opened <- err
	}()
	time.Sleep(time.Second)
	_, err := os.Stat(marker)
	resume()
	if err == nil {
		t.Error("a plugin was launched once the exit had begun")
	}
	<-opened
}

func pidWritten(t *testing.T, path string) int {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		raw, err := os.ReadFile(path)
		if err == nil && strings.HasSuffix(string(raw), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("the plugin wrote %q for its pid", raw)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the plugin never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
