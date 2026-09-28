//go:build windows

package plugin

import "syscall"

// The operating system's errors DialRefused and DialUnroutable read a dial
// by, as Winsock numbers them. Not syscall's ECONNREFUSED and the rest: on
// Windows those are values Go invented for its own use, which no socket call
// returns — a refused dial arrives as WSAECONNREFUSED, and asked of them it
// would read as no refusal at all. syscall names only a few of Winsock's
// errors, so these are the numbers Winsock documents.
var (
	refusedErrnos    = []syscall.Errno{10061} // WSAECONNREFUSED
	unroutableErrnos = []syscall.Errno{
		10051, // WSAENETUNREACH
		10065, // WSAEHOSTUNREACH
		10064, // WSAEHOSTDOWN
		10050, // WSAENETDOWN
	}
)
