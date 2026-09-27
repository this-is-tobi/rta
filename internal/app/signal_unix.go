//go:build !windows

package app

import "golang.org/x/sys/unix"

// foreground reports whether this process's group is the one the terminal on
// fd is talking to.
func foreground(fd int) bool {
	group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	return err == nil && group == unix.Getpgrp()
}
