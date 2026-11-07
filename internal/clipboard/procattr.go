package clipboard

import (
	"os/exec"
	"syscall"

	"github.com/this-is-tobi/rta/internal/procgroup"
)

// harden puts a clipboard program in a process group of its own
// (procgroup.Harden), so a kill on timeout reaches whatever it forked and not
// just itself.
//
// xclip's normal, successful path already forks into the background to keep
// serving the X11 selection after its own parent exits — see Commands' doc
// comment. A program stuck instead of exiting cleanly may have done the same
// forking on its way to getting stuck, and exec.CommandContext's default
// Cancel only reaches the direct child: measured, a stand-in program that
// forked before hanging left its child running after Copy returned, every
// time, until this.
func harden(cmd *exec.Cmd) { procgroup.Harden(cmd) }

// reap kills the whole process group a hardened command created. SIGKILL
// rather than the tunnel's SIGTERM: a clipboard program is a short-lived
// stdin-to-clipboard utility with no network resource or remote state to close
// gracefully, and one still running once the full timeout has elapsed is either
// ignoring signals or was never going to exit on its own.
func reap(cmd *exec.Cmd) { procgroup.Signal(cmd, syscall.SIGKILL) }
