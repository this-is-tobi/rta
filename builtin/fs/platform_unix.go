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

// fileID names a file by device and inode, to count one with several names
// once.
type fileID struct{ dev, ino uint64 }

// diskUsage is what a file takes on disk, in the 512-byte units stat counts
// it in whatever the filesystem's block size is, and whether it has other
// names (a link count above one), with the identity to count it once by.
//
// A FileInfo from something other than the OS filesystem has no stat to read
// and is counted by its length.
func diskUsage(info os.FileInfo) (size int64, id fileID, shared bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return info.Size(), fileID{}, false
	}
	//nolint:unconvert,gosec // Dev, Ino and Nlink are different widths and signs on darwin and linux; they are identities compared for equality, and a count compared with one.
	return int64(st.Blocks) * 512, fileID{uint64(st.Dev), uint64(st.Ino)}, st.Nlink > 1
}
