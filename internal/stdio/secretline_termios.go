//go:build darwin || linux

package stdio

import "golang.org/x/sys/unix"

// readTerminalLine reads one line from the terminal on fd with echo off and
// the line discipline off, its editing done here rather than there
// (lineKeys), and puts the terminal back as it was.
func readTerminalLine(fd int) ([]byte, error) {
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	t := *old
	t.Lflag &^= unix.ECHO | unix.ICANON
	t.Lflag |= unix.ISIG
	t.Iflag |= unix.ICRNL
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &t); err != nil {
		return nil, err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }()

	return keysOf(old).readLine(terminalReader(fd))
}

// keysOf reads the line-editing keys off the terminal's settings t.
//
// A key the terminal has switched off (`stty werase undef`) holds the
// platform's _POSIX_VDISABLE, vdisable here, and is read as not set. On
// Linux that is zero, which lineKeys already reads so; on macOS it is 0xff,
// and read as a key it made a byte of 0xff — a Latin-1 terminal's y with a
// diaeresis — erase the word before it, where the discipline takes a key
// switched off as no key at all.
func keysOf(t *unix.Termios) lineKeys {
	key := func(i int) byte {
		if c := t.Cc[i]; c != vdisable {
			return c
		}
		return 0
	}
	return lineKeys{erase: key(unix.VERASE), kill: key(unix.VKILL), wordErase: key(unix.VWERASE),
		eof: key(unix.VEOF), utf8: t.Iflag&unix.IUTF8 != 0}
}

// terminalReader reads fd directly, the way term.ReadPassword does, rather
// than through an *os.File, whose reads the runtime would poll.
type terminalReader int

func (r terminalReader) Read(b []byte) (int, error) {
	n, err := unix.Read(int(r), b)
	return max(n, 0), err
}
