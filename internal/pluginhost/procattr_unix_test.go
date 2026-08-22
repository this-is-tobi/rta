//go:build !windows

package pluginhost

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// go-plugin kills the process it started and nothing else. A plugin that
// shells out — which is most of the interesting ones, and the entire exec
// tier — leaves its children running when it dies: still holding sockets,
// still holding the terminal, invisible to anything rta reports.
//
// This is the test that distinguishes reap(-pgid) from Kill(pid). Both make
// the plugin itself exit, so a test that only checked the plugin would pass
// against the broken version.
func TestReapTakesTheWholeProcessTree(t *testing.T) {
	id, err := Identify("/bin/sh")
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
	cmd := buildCmd(id, deny, []string{"-c", "sleep 60 >/dev/null 2>&1 & echo $!; exit 0"})
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
	cmd := buildCmd(id, deny, []string{"-c", "sleep 5"})
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
