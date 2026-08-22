//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
)

// harden puts kubectl in its own process group so the whole group can be
// signalled — the same reason internal/pluginhost does it. A port-forward
// that outlives its call is a hole in a cluster's network boundary that
// nobody is watching.
func harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// reap signals the whole group, not just the leader.
//
// The group id is the child's own pid, read from the command rta configured,
// and never the answer to Getpgid. internal/pluginhost/procattr_unix.go
// carries the full argument and this package had the bug that comment
// describes; the two reasons it matters here are both on the common path.
//
// A waited-on pid is not a live process. Every failed open closes its tunnel,
// and by then the goroutine watching kubectl has usually returned from Wait,
// so the pid is already released — Getpgid answers ESRCH and the fallback
// kills something that has already exited, or, if the kernel has handed that
// pid to somebody else in between, answers with a *stranger's* group and rta
// SIGTERMs it.
//
// A command rta did not harden has no group of its own. Negating its pid
// signals whatever group it shares, which for anything started outside this
// package is rta's own — so the shell that launched rta, and every job in it,
// receives the signal. Deciding from SysProcAttr rather than from a syscall
// is what makes that unreachable rather than unlikely.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 1 || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		_ = cmd.Process.Kill()
		return
	}
	// SIGTERM, not SIGKILL: kubectl closes its streams and tells the API
	// server the forward is over, which SIGKILL leaves to a timeout.
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = cmd.Process.Kill()
	}
}
