package clipboard

import (
	"os/exec"
	"syscall"
)

// harden puts the child in its own process group, the same partial
// mitigation internal/pluginhost uses for the same reason: one handle, one
// signal, no stronger guarantee than that without job-object plumbing this
// package does not carry. Stated here rather than left an empty function,
// so the gap is documented rather than silent.
func harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// reap kills the child. Without a job object this reaches the process and
// not necessarily whatever it forked — see harden.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
