//go:build !windows

package plugin

import "syscall"

// The operating system's errors DialRefused and DialUnroutable read a dial
// by, as a Unix names them. EHOSTDOWN is macOS's answer for a host on its own
// network that nothing answers for, where Linux says EHOSTUNREACH.
var (
	refusedErrnos    = []syscall.Errno{syscall.ECONNREFUSED}
	unroutableErrnos = []syscall.Errno{syscall.ENETUNREACH, syscall.EHOSTUNREACH, syscall.EHOSTDOWN, syscall.ENETDOWN}
)
