//go:build darwin

package tunnel

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
)

// forwardsDir is where a forward in flight is written down, one file a forward
// named for its pid.
func forwardsDir() string { return filepath.Join(paths.Data(), "forwards") }

// A forward is the process that was started and when the kernel says it began,
// which is what tells it from whatever is given its pid afterwards, and the
// process that started it, identified the same way: a forward is an orphan
// only once that one is gone, and other servers on the machine are alive.
//
// The name it runs under is not part of the answer, though the kernel reports
// one: it is not stable over a process's life (a shell script started as sh
// reports bash once the shell has set its own), and the start time, to the
// microsecond, is the kernel's own and is not given to two processes.
type forward struct {
	PID          int   `json:"pid"`
	Started      int64 `json:"started"`
	Owner        int   `json:"owner"`
	OwnerStarted int64 `json:"owner_started"`
}

// identity is the kernel's own account of the process with pid: when it began,
// in microseconds, and its group. Read, not asked of a child that could say
// anything.
func identity(pid int) (started int64, pgid int, ok bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || int(kp.Proc.P_pid) != pid {
		return 0, 0, false
	}
	tv := kp.Proc.P_starttime
	return tv.Sec*1_000_000 + int64(tv.Usec), int(kp.Eproc.Pgid), true
}

func recordPath(pid int) string { return filepath.Join(forwardsDir(), strconv.Itoa(pid)+".json") }

// remember writes a forward down for the next start to find, and forget takes
// it back when the forward has ended.
//
// **macOS has no parent-death signal, so a forward whose parent was killed
// outright is a listener on a loopback port into the operator's cluster that
// nothing is watching** — a SIGKILL, the OOM killer, a crash in the middle of a
// call. What this process cannot do for itself the next one does: the pid, the
// start time the kernel reports for it and its name, in a file under the data
// directory, which ReapOrphans checks against the live process before it
// signals anything. Best effort, and said nowhere when it fails: the forward
// opens either way, and what is lost is the safety net.
func remember(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	started, _, ok := identity(cmd.Process.Pid)
	if !ok {
		return
	}
	ownerStarted, _, _ := identity(os.Getpid())
	body, err := json.Marshal(forward{PID: cmd.Process.Pid, Started: started,
		Owner: os.Getpid(), OwnerStarted: ownerStarted})
	if err != nil {
		return
	}
	if err := os.MkdirAll(forwardsDir(), 0o700); err != nil {
		return
	}
	_ = atomicfile.Write(recordPath(cmd.Process.Pid), body, 0o600)
}

func forget(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = os.Remove(recordPath(cmd.Process.Pid))
}

// ReapOrphans stops the forwards a previous process left running and says how
// many it stopped.
//
// A forward is stopped only when the process that started it is gone and the
// process at its pid is still the one that was written down: the same start
// time to the microsecond. A pid the kernel has handed to
// something else since is a recycled one, and is never signalled; the record
// of it is removed. A forward whose owner is alive is another server's, and is
// left alone with its record. The group is signalled only when the process
// leads its own, and never this process's: a record is a file in a directory
// something else may write, and what is in it must not be a way to have rta
// signal what it likes.
func ReapOrphans() int {
	entries, err := os.ReadDir(forwardsDir())
	if err != nil {
		return 0
	}
	stopped := 0
	for _, e := range entries {
		path := filepath.Join(forwardsDir(), e.Name())
		raw, err := atomicfile.ReadCapped(path, 1<<10)
		if err != nil {
			_ = os.Remove(path)
			continue
		}
		var f forward
		if json.Unmarshal(raw, &f) != nil || f.PID <= 1 || !strings.HasSuffix(e.Name(), ".json") ||
			e.Name() != fmt.Sprintf("%d.json", f.PID) {
			_ = os.Remove(path)
			continue
		}
		if ownerStarted, _, alive := identity(f.Owner); alive && f.Owner > 0 && ownerStarted == f.OwnerStarted {
			continue
		}
		_ = os.Remove(path)
		started, pgid, ok := identity(f.PID)
		if !ok || started != f.Started || f.PID == os.Getpid() {
			continue
		}
		if pgid == f.PID && pgid != syscall.Getpgrp() {
			_ = syscall.Kill(-f.PID, syscall.SIGTERM)
		} else {
			_ = syscall.Kill(f.PID, syscall.SIGTERM)
		}
		stopped++
	}
	return stopped
}
