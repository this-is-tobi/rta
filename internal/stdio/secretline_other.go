//go:build !darwin && !linux

package stdio

import "golang.org/x/term"

// readTerminalLine reads the line as ReadSecret does, and reads nothing after
// it: on Windows the console, not a terminal's settings, decides how a line
// is read, and what is still arriving is left in its input buffer for
// whatever reads the console next. A caller that must not take the first
// line of a longer value for the whole still has to look at the line.
func readTerminalLine(fd int) ([]byte, bool, error) {
	line, err := readTerminalSecret(fd)
	return line, false, err
}

// readTerminalSecret is term.ReadPassword where rta does not set the
// terminal's mode itself: the console, or a terminal whose settings this
// package does not read.
func readTerminalSecret(fd int) ([]byte, error) { return term.ReadPassword(fd) }
