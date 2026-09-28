package stdio

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
	// vdisable is macOS's _POSIX_VDISABLE, what a key switched off holds.
	vdisable = 0xff
)
