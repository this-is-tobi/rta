package tunnel

import (
	"os/exec"
	"syscall"

	"github.com/this-is-tobi/rta/internal/procgroup"
)

// harden puts kubectl or ssh in a process group of its own (procgroup.Harden)
// so the whole group can be signalled, and ties its life to rta's where the
// platform can (deathSignal). A port-forward that outlives its call is a hole
// in a cluster's network boundary that nobody is watching.
func harden(cmd *exec.Cmd) {
	procgroup.Harden(cmd)
	deathSignal(cmd.SysProcAttr)
}

// reap signals the whole group, not just the leader. SIGTERM, not SIGKILL:
// kubectl closes its streams and tells the API server the forward is over,
// which SIGKILL leaves to a timeout.
//
// Both reasons the group id must be the child's own pid are on this package's
// common path (procgroup.Signal): every failed open closes its tunnel after
// the goroutine watching kubectl has usually returned from Wait, and a command
// rta did not harden negated would signal the shell that launched rta.
func reap(cmd *exec.Cmd) { procgroup.Signal(cmd, syscall.SIGTERM) }

// force is reap without the grace: SIGKILL to the whole group, for a kubectl
// or ssh that was asked to end and did not. A forward that ignores SIGTERM
// keeps listening on a port the caller believes is closed, and nothing is
// watching it.
func force(cmd *exec.Cmd) { procgroup.Signal(cmd, syscall.SIGKILL) }
