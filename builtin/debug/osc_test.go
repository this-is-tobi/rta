package debug

import (
	"strings"
	"testing"
)

// An OSC that asks the terminal a question is named for the answer it brings
// back into the input, and the commands a shell integration, a prompt or a
// notification sends are named for what they do. Each was "OSC 11", "OSC 133"
// and so on: a number, in the one tool that exists to say what a sequence is.
func TestAnOSCIsNamedForWhatItAsksOrDoes(t *testing.T) {
	for _, c := range []struct{ input, want string }{
		{"\x1b]11;?\x07", "report the background colour — the terminal answers into the input"},
		{"\x1b]10;?\x1b\\", "report the foreground colour — the terminal answers"},
		{"\x1b]12;#ff0000\x07", "set the cursor colour to #ff0000"},
		{"\x1b]4;1;?\x07", "report palette colour 1 — the terminal answers"},
		{"\x1b]4;1;rgb:ff/00/00;2;?\x07", "set palette colour 1; report palette colour 2"},
		{"\x1b]104\x07", "reset the palette colours"},
		{"\x1b]111\x07", "reset the background colour"},
		{"\x1b]7;file://host/tmp/x\x07", "set the working directory: file://host/tmp/x"},
		{"\x1b]133;A\x07", "shell integration mark: A"},
		{"\x1b]9;build done\x07", "desktop notification (iTerm2): build done"},
		{"\x1b]9;4;1;50\x07", "progress report (ConEmu, Windows Terminal): 1;50"},
		{"\x1b]777;notify;title;body\x07", "desktop notification (rxvt): title;body"},
		{"\x1b]1337;File=inline=1:AAAA\x07", "iTerm2 inline file"},
		{"\x1b]8;;\x1b\\", "end of hyperlink"},
	} {
		if got := meaningOf(t, c.input); !strings.Contains(got, c.want) {
			t.Errorf("%q: %q, want it to say %q", c.input, got, c.want)
		}
	}
}

// A command nothing names keeps its number, as it always did.
func TestAnOSCNothingNamesKeepsItsNumber(t *testing.T) {
	if got := meaningOf(t, "\x1b]5555;x\x07"); got != "OSC 5555" {
		t.Errorf("got %q, want the bare number", got)
	}
}
