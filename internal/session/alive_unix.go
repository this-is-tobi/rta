//go:build !windows

package session

import (
	"errors"
	"os"
	"syscall"
)

// alive asks the kernel whether a process exists without touching it: signal
// zero delivers nothing and fails with ESRCH when there is nobody to deliver
// to. EPERM means the process exists and is not ours, which for presence is
// still "alive".
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}
