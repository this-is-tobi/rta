package stdio

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// openPTY opens a pseudo-terminal: master, the side a terminal emulator
// holds, and terminal, the side a program reads from, as glibc's
// posix_openpt, unlockpt and ptsname do it on Linux: /dev/ptmx, its lock
// taken off, and the /dev/pts entry the number it is given names. Neither
// becomes the test's controlling terminal.
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
	if err = unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		return -1, -1, fmt.Errorf("unlocking it: %w", err)
	}
	n, err := unix.IoctlGetUint32(master, unix.TIOCGPTN)
	if err != nil {
		return -1, -1, fmt.Errorf("asking its number: %w", err)
	}
	terminal, err = unix.Open(fmt.Sprintf("/dev/pts/%d", n), unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, -1, err
	}
	return master, terminal, nil
}
