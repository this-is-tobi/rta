//go:build !darwin && !linux

package stdio

// readTerminalLine reads the line as ReadSecret does: on Windows the
// console, not a terminal's settings, decides how a line is read.
func readTerminalLine(fd int) ([]byte, error) { return readPassword(fd) }
