// Package procgroup holds the rules for the programs rta starts that may fork
// (a plugin, a clipboard program, kubectl, ssh, a package manager): a process
// group of the command's own, a signal that reaches the whole group, and a
// bound on how long the command may hold rta's pipes once it is gone. Each of
// those packages carried its own copy of these, three of them hash-identical,
// with comments recording that a rule had already been got wrong once; one
// copy is the one place to get it right.
package procgroup

import (
	"os/exec"
	"syscall"
	"time"
)

// WaitDelay bounds how long a program rta started may hold its pipes once it
// has exited or been killed. Every command that may fork sets it
// (cmd.WaitDelay = procgroup.WaitDelay).
//
// A program that forks and exits leaves the child holding rta's end of its
// stdout or stderr: a kubeconfig's exec credential helper (kubelogin shelling
// out to a browser for an OIDC device flow is the concrete one), a package
// manager's background auto-update, a version manager's shim. Cmd.Wait with
// WaitDelay unset ends in an unguarded receive on the copier's channel, so it
// blocks until every pipe sees EOF, which that child is holding: not slow,
// *forever*, and past every deadline the caller set, because it is os/exec's
// copying goroutines that are stuck rather than the process.
// exec.CommandContext does not save it: cancelling kills the program and
// does not close the pipes.
//
// It cannot cut a healthy child short, since it only starts counting once the
// process has exited or the context has killed it. A run that meets it
// returns exec.ErrWaitDelay, which means the program itself finished.
const WaitDelay = 2 * time.Second

// Harden sets what must be set at spawn time, because none of it can be applied
// to a process that is already running: a process group of the command's own,
// so that one signal takes everything it started.
//
// A program that shells out leaves its children behind when it alone is killed,
// still holding the terminal, sockets, a port-forward into a cluster, invisible
// to anything rta reports. In a group of its own, the group is the unit Signal
// reaches.
//
// Deliberately NOT Setsid. A session leader detaches from the controlling
// terminal, which breaks the one thing such a program may legitimately want:
// prompting for a passphrase. Setpgid gives the reaping without the detachment.
func Harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Signal sends sig to the whole process group a hardened command created.
//
// The negative pid is the group. Kill(-pgid) rather than Kill(pid) is the
// entire point of Harden, and calling the wrong one leaves exactly the orphans
// it exists to prevent, which looks identical in every test that only asserts
// the command itself exited.
//
// The group id is the child's own pid, read from the command rta configured,
// and never the answer to Getpgid. Setpgid with a zero Pgid makes the child the
// leader of a new group whose id equals its pid, so the value is known at spawn
// time and stays valid afterwards. Asking the kernel instead is what the first
// versions did, and it is wrong precisely when reaping matters most: once the
// child has been waited on its pid is no longer a live process, Getpgid answers
// ESRCH, and the fallback kills a process that has already exited while its
// orphaned children keep running (measured: a backgrounded `sleep` survived
// every time), or, if the kernel has handed that pid to somebody else in
// between, answers with a stranger's group, which then receives the signal.
//
// A command that was never hardened is killed directly. Negating a pid whose
// group rta did not create would signal whatever group it happens to share, and
// for a process started elsewhere that group is rta's own: the shell that
// launched rta and every job in it. Deciding from SysProcAttr rather than from
// a syscall is what makes that unreachable rather than unlikely.
func Signal(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 1 || cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		_ = cmd.Process.Kill()
		return
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = cmd.Process.Kill()
	}
}
