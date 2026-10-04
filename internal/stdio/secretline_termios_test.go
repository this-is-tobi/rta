package stdio

import (
	"testing"

	"golang.org/x/sys/unix"
)

// A key the terminal has switched off holds the platform's _POSIX_VDISABLE,
// and is read as not set, so a byte of that value is typed like any other
// (TestALineIsEditedAsTheTerminalWouldHaveEditedIt, for a key not set): on
// macOS, where it is 0xff, `stty werase undef` made that byte erase the word
// before it.
func TestAKeyTheTerminalSwitchedOffIsNotSet(t *testing.T) {
	var settings unix.Termios
	settings.Cc[unix.VERASE] = 0x7f
	settings.Cc[unix.VKILL] = vdisable
	settings.Cc[unix.VWERASE] = vdisable
	settings.Cc[unix.VEOF] = 0x04
	settings.Iflag = unix.IUTF8
	want := lineKeys{erase: 0x7f, eof: 0x04, utf8: true}
	if got := keysOf(&settings); got != want {
		t.Errorf("keysOf read %+v, want %+v", got, want)
	}
}
