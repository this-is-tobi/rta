//go:build unix

package pathguard

import (
	"io/fs"
	"syscall"
)

// fileID is a file's identity: the device it is on and its number there,
// which every name of the file shares.
type fileID struct{ dev, ino uint64 }

// identity is the device and inode number info names its file by, and false
// for a FileInfo that carries none: one from something other than the
// operating system's filesystem.
func identity(info fs.FileInfo) (fileID, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, false
	}
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, true //nolint:unconvert,gosec // Dev is int32 on darwin and uint64 on linux, Ino uint64 on both and narrower elsewhere; the conversions are what builds on all of them, and each is an identity compared for equality, never arithmetic, so a sign-wrapped value is as unique as the original.
}
