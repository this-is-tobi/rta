//go:build darwin

package git

import "golang.org/x/sys/unix"

// lchmod sets the mode of the symbolic link at path itself, which macOS keeps
// apart from the mode of what it leads to.
func lchmod(path string, mode uint32) error {
	return unix.Fchmodat(unix.AT_FDCWD, path, mode, unix.AT_SYMLINK_NOFOLLOW)
}
