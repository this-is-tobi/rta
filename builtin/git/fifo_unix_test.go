//go:build unix

package git

import "syscall"

func mkfifo(path string) error { return syscall.Mkfifo(path, 0o644) }
