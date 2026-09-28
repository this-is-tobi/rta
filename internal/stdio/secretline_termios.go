//go:build darwin || linux

package stdio

import "golang.org/x/sys/unix"

// drainWindow is how long, in tenths of a second, the terminal is read after
// the line for the rest of a paste before it counts as quiet: the terminal's
// VTIME, which a read with VMIN at zero waits for a byte before it returns
// none.
//
// Measured on a pseudo-terminal with a 64 KiB kubeconfig pasted after its
// first line. Written whole, as a terminal emulator writes a paste, it
// arrived within 6 ms, never more than 0.2 ms apart; written in paced
// pieces, 1 KiB every 10 ms or 4 KiB every 50 ms, never more than 56 ms
// apart, and every byte was read at a window of 100 ms. 200 ms leaves that
// margin again over the slowest, and is the wait a value typed and ended
// with the return key costs before the prompt returns.
const drainWindow = 2

// readTerminalLine reads one line from the terminal on fd with echo off, the
// line discipline's editing done here rather than there (lineKeys), then
// reads what is still arriving until the terminal is quiet for drainWindow,
// and puts the terminal back as it was.
//
// Put back without flushing its input: a flush also waits for the output
// already written to reach the terminal, and a terminal whose other end has
// stopped reading would hold the prompt there. What arrives after the
// window is the next thing typed, not the paste.
func readTerminalLine(fd int) ([]byte, bool, error) { return readTerminal(fd, true) }

// readTerminalSecret reads one line as readTerminalLine does and nothing
// after it: one byte at a time, so what was typed ahead of the prompt that
// follows — a passphrase's "Once more:" — stays on the terminal for it, as
// it did when the discipline read the line.
func readTerminalSecret(fd int) ([]byte, error) {
	line, _, err := readTerminal(fd, false)
	return line, err
}

func readTerminal(fd int, drainPaste bool) ([]byte, bool, error) {
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, false, err
	}
	t := *old
	t.Lflag &^= unix.ECHO | unix.ICANON
	t.Lflag |= unix.ISIG
	t.Iflag |= unix.ICRNL
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &t); err != nil {
		return nil, false, err
	}
	defer func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, old) }()

	if !drainPaste {
		line, _, err := keysOf(old).readLine(byteReader{terminalReader(fd)})
		return line, false, err
	}
	line, rest, err := keysOf(old).readLine(terminalReader(fd))
	if err != nil {
		return line, false, err
	}
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 0, drainWindow
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &t); err != nil {
		return line, !blank(rest), nil
	}
	more := !blank(rest)
	clear(rest)
	return line, drain(terminalReader(fd)) || more, nil
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
