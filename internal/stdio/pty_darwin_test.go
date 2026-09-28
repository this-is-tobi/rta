package stdio

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal: master, the side a terminal emulator
// holds, and terminal, the side a program reads from, as macOS's
// posix_openpt, grantpt, unlockpt and ptsname do it: /dev/ptmx, and the ioctls
// behind each of the others. Neither becomes the test's controlling terminal.
//
// The name comes back in a buffer of 128 bytes the kernel writes, which no
// helper in x/sys/unix fills, and libc's ptsname, which fills it, is cgo, so
// that one ioctl is made as a direct system call, which x/sys marks
// deprecated on macOS in favour of the libc wrappers it does not have for
// this one. The name could be guessed from the minor number of the master
// instead, /dev/ttys and three digits; a guess that ever went wrong would
// open, and change the settings of, another session's terminal.
func openPTY() (master, terminal int, err error) {
	master, err = unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, -1, err
	}
	defer func() {
		if err != nil {
			_ = unix.Close(master)
		}
	}()
	if err = unix.IoctlSetInt(master, unix.TIOCPTYGRANT, 0); err != nil {
		return -1, -1, fmt.Errorf("granting it: %w", err)
	}
	if err = unix.IoctlSetInt(master, unix.TIOCPTYUNLK, 0); err != nil {
		return -1, -1, fmt.Errorf("unlocking it: %w", err)
	}
	var name [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(master), unix.TIOCPTYGNAME, //nolint:staticcheck // see above
		uintptr(unsafe.Pointer(&name[0]))); errno != 0 {
		err = errno
		return -1, -1, fmt.Errorf("asking its name: %w", err)
	}
	terminal, err = unix.Open(unix.ByteSliceToString(name[:]), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, -1, err
	}
	return master, terminal, nil
}
