package pluginhost

import (
	"os/exec"
	"syscall"
)

// harden puts the child in its own process group so a kill reaches the tree.
//
// CREATE_NEW_PROCESS_GROUP is the Windows analogue of Setpgid for the purpose
// that matters here — one handle, one kill, no orphans. A job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE is the stronger form and is what ADR
// 0012 names; it needs the golang.org/x/sys/windows job APIs and a handle held
// for the process's lifetime, which is a real amount of Windows-specific
// plumbing for a platform that is documented as unconfined either way.
//
// This is the honest intermediate: it is stated as lifetime control, it is not
// stated as confinement, and the job-object upgrade is additive when somebody
// runs rta on Windows in earnest. What it must not become is a silent gap —
// hence this comment rather than an empty function.
func harden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// reap kills the child. Without a job object this reaches the process and not
// necessarily its descendants — see harden.
func reap(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
