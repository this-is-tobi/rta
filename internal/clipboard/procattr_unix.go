//go:build !windows

package clipboard

import (
	"os/exec"
	"syscall"
)

// harden puts a clipboard program in its own process group, so a kill on
// timeout reaches whatever it forked and not just itself — the same reason
// internal/pluginhost and internal/tunnel set Setpgid on their own
// shell-outs.
//
// xclip's normal, successful path already forks into the background to keep
// serving the X11 selection after its own parent exits — see Commands' doc
// comment. A program stuck instead of exiting cleanly may have done the
// same forking on its way to getting stuck, and exec.CommandContext's
// default Cancel only reaches the direct child: measured, a stand-in
// program that forked before hanging left its child running after Copy
// returned, every time, until this.
//
// Deliberately NOT Setsid, matching the other two packages' own reasoning
// for the same call: a session leader detaches from the controlling
// terminal, and Setpgid gets the reaping without the detachment.
func harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// reap kills the whole process group a hardened command created, the same
// way and for the same reason as internal/pluginhost's and internal/tunnel's
// own reap: the negative pid is the group, and killing the bare pid instead
// leaves exactly the orphans Setpgid exists to let this clean up.
//
// SIGKILL rather than tunnel's SIGTERM: a clipboard program is a short-lived
// stdin-to-clipboard utility with no network resource or remote state to
// close gracefully, and one still running once the full timeout has elapsed
// is either ignoring signals or was never going to exit on its own.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 1 || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		_ = cmd.Process.Kill()
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}
