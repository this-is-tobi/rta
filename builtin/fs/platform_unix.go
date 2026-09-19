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
	return uint64(st.Dev), true //nolint:unconvert,gosec // Dev is int32 on darwin and uint64 on linux/amd64; the conversion is what builds on both, and redundant on the one CI lints — and Dev is an identity compared for equality, never arithmetic, so a sign-wrapped darwin value is as unique as the original.
}
