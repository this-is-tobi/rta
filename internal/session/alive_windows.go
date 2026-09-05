//go:build windows

package session

import "os"

// alive: on Windows FindProcess opens a handle to the process and fails when
// there is none, which is the whole check.
func alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
