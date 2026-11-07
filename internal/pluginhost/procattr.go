package pluginhost

import (
	"os/exec"
	"syscall"

	"github.com/this-is-tobi/rta/internal/procgroup"
)

// harden puts a plugin in a process group of its own (procgroup.Harden).
// go-plugin kills the process it started and nothing else, and a plugin that
// shells out — the entire exec tier and most of the interesting ones — leaves
// its children behind when it dies.
//
// It is also why this is a first-commit item rather than a later improvement:
// adding it afterwards means revisiting every spawn site, every kill path and
// every test that assumed a bare exec.Cmd, and the kill path is the one nobody
// exercises until it matters.
func harden(cmd *exec.Cmd) { procgroup.Harden(cmd) }

// reap kills the whole process group a hardened plugin created. SIGKILL: the
// plugin already had its chance at a graceful shutdown through go-plugin's
// Kill, and anything still here after that is either ignoring signals or was
// never told.
func reap(cmd *exec.Cmd) { procgroup.Signal(cmd, syscall.SIGKILL) }
