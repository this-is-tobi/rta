//go:build !windows

package pluginhost

import (
	"os/exec"
	"syscall"
)

// harden sets what must be set at spawn time, because none of it can be
// applied to a process that is already running.
//
// **Setpgid.** go-plugin kills the process it started and nothing else. A
// plugin that shells out — which is the entire exec tier and most of the
// interesting ones — leaves its children behind when it dies, still holding
// the terminal, still holding sockets, invisible to anything rta reports. Put
// the plugin in its own process group and one kill takes the tree.
//
// It is also why this is a first-commit item rather than a later improvement:
// adding it afterwards means revisiting every spawn site, every kill path and
// every test that assumed a bare exec.Cmd, and the kill path is the one nobody
// exercises until it matters.
//
// Deliberately NOT Setsid. A session leader detaches from the controlling
// terminal, which breaks the one thing an exec-tier plugin may legitimately
// want: prompting for a passphrase. Setpgid gives the reaping without the
// detachment.
func harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// reap kills the whole process group a hardened command created.
//
// The negative pid is the group. Kill(-pgid) rather than Kill(pid) is the
// entire point of Setpgid above, and calling the wrong one leaves exactly the
// orphans this exists to prevent — which looks identical in every test that
// only asserts the plugin itself exited.
//
// The group id is the child's own pid, not the answer to Getpgid. Setpgid
// with a zero Pgid makes the child the leader of a new group whose id equals
// its pid, so the value is known at spawn time and stays valid afterwards.
// Asking the kernel instead is what the first version did, and it is wrong
// precisely when reaping matters most: once the child has been waited on, its
// pid is no longer a live process, Getpgid returns ESRCH, and the fallback
// kills a process that has already exited while its orphaned children keep
// running. Measured — a backgrounded `sleep` survived reap every time.
//
// A command that was never hardened is killed directly. Negating a pid whose
// group rta did not create would signal whatever group it happens to share,
// and for a process started outside this package that group is rta's own.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 1 || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		_ = cmd.Process.Kill()
		return
	}
	// SIGKILL to the group. The plugin already had its chance at a graceful
	// shutdown through go-plugin's Kill; anything still here after that is
	// either ignoring signals or was never told.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
