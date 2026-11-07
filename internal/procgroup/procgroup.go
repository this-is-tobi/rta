// Package procgroup starts a command in a process group of its own and signals
// that group, for the programs rta starts that may fork: a plugin, a clipboard
// program, kubectl and ssh. Each of those packages carried its own copy of both
// functions, three of them hash-identical, with comments recording that the
// rule below had already been got wrong once; one copy is the one place to get
// it right.
package procgroup

import (
	"os/exec"
	"syscall"
)

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
