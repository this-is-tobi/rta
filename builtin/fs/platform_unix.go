//go:build !windows

package fs

import (
	"os"
	"syscall"
)

// deviceOfInfo reads the device number out of the stat result. Available on
// every unix Go builds for; the assertion is comma-ok anyway, because a
// FileInfo from something other than the OS filesystem carries a different
// Sys().
func deviceOfInfo(info os.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
