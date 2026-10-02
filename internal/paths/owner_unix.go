//go:build unix

package paths

import (
	"io/fs"
	"os"
	"syscall"
)

// ownedByUs reports that info names a file this account owns. A FileInfo that
// carries no owner is not vouched for.
func ownedByUs(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
