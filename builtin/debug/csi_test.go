package debug

import (
	"strings"
	"testing"
)

// meaningOf is what the Meaning column says of the one sequence input holds.
func meaningOf(t *testing.T, input string) string {
	t.Helper()
	table := explainAnsi(input)
	if table.Total != 1 {
		t.Fatalf("%q: %d rows, want the one sequence: %v", input, table.Total, table.Rows)
	}
	return table.Rows[0][2]
}

// A sequence is named by its prefix, intermediate and final byte together. Read
// by the final alone, ESC[>4;1m (xterm's modifyOtherKeys) was "underline,
// bold", ESC[=1u (the kitty keyboard protocol) a restored cursor, and ESC#8
// (fill the screen with E) a restored cursor too.
func TestASequenceIsNamedByItsPrefixAndIntermediateNotItsFinalAlone(t *testing.T) {
	for _, c := range []struct {
		input, want, not string
	}{
		{"\x1b[>4;1m", "modifyOtherKeys = 1", "underline"},
		{"\x1b[>4m", "modifyOtherKeys: reset", "underline"},
		{"\x1b[=1u", "kitty keyboard protocol: set", "restore cursor"},
		{"\x1b[>1u", "kitty keyboard protocol: push", "restore cursor"},
		{"\x1b[<1u", "kitty keyboard protocol: pop 1", "restore cursor"},
		{"\x1b[?u", "kitty keyboard protocol: query", "restore cursor"},
		{"\x1b#8", "screen alignment test", "restore cursor"},
		{"\x1b#3", "double-height line, top half", ""},
		{"\x1b[?2J", "selectively erase entire screen", ""},
		{"\x1b[?1K", "selectively erase from start of line", ""},
		{"\x1b[2 q", "cursor style: steady block", ""},
		{"\x1b[!p", "soft terminal reset", ""},
		{"\x1b[?25$p", "request the state of private mode 25", ""},
		// The plain neighbours keep their own meaning.
		{"\x1b[1;31m", "bold, red foreground", ""},
		{"\x1b[2J", "erase entire screen", "selectively"},
		{"\x1b[u", "restore cursor position", ""},
		{"\x1b8", "restore cursor position", ""},
		{"\x1b7", "save cursor position", ""},
	} {
		got := meaningOf(t, c.input)
		if !strings.Contains(got, c.want) || (c.not != "" && strings.Contains(got, c.not)) {
			t.Errorf("%q: %q, want it to say %q and not %q", c.input, got, c.want, c.not)
		}
	}
}

// A parameter with colon-separated sub-parameters is one parameter. 4:3 is a
// curly underline, which neovim and vim send for a spelling error, and was
// "underline, italic"; 38:2::R:G:B has an empty colour-space field, and was a
// colour of the wrong channels followed by a black foreground.
func TestSGRSubParametersAreOneParameter(t *testing.T) {
	for input, want := range map[string]string{
		"\x1b[4:3m":                  "curly underline",
		"\x1b[4:0m":                  "no underline",
		"\x1b[4:5m":                  "dashed underline",
		"\x1b[38:2::10:20:30m":       "foreground (rgb 10,20,30)",
		"\x1b[38:2:0:10:20:30m":      "foreground (rgb 10,20,30)",
		"\x1b[38:2:10:20:30m":        "foreground (rgb 10,20,30)",
		"\x1b[48:5:196m":             "background (256-color 196)",
		"\x1b[58:2::1:2:3m":          "underline color (rgb 1,2,3)",
		"\x1b[1;4:3;38;5;9m":         "bold, curly underline, foreground (256-color 9)",
		"\x1b[4:3;58:2::255:0:0m":    "curly underline, underline color (rgb 255,0,0)",
		"\x1b[38;2;1;2;3;1m":         "foreground (rgb 1,2,3), bold",
		"\x1b[38;5;196m":             "foreground (256-color 196)",
		"\x1b[4m":                    "underline",
		"\x1b[7:1m":                  "SGR 7:1",
		"\x1b[38:9:1:2m":             "SGR 38:9:1:2",
		"\x1b[1;38:2::1:2:3;48:5:7m": "bold, foreground (rgb 1,2,3), background (256-color 7)",
	} {
		if got := meaningOf(t, input); got != want {
			t.Errorf("%q: %q, want %q", input, got, want)
		}
	}
}

// What a program leaves set when it garbles a terminal is named for what it
// does, in words a person can match against the symptom.
func TestTheModesAProgramLeavesSetAreNamed(t *testing.T) {
	for input, want := range map[string]string{
		"\x1b[?25l":        "hide the cursor",
		"\x1b[?25h":        "show the cursor",
		"\x1b[?1049h":      "switch to the alternate screen, saving the cursor",
		"\x1b[?1049l":      "leave the alternate screen, restoring the cursor",
		"\x1b[?2004h":      "enable bracketed paste",
		"\x1b[?1000;1006h": "enable mouse button reporting, enable SGR mouse encoding",
		"\x1b[?1000l":      "disable mouse button reporting",
		"\x1b[?99999h":     "enable private mode 99999",
		"\x1b[4h":          "enable insert mode",
		"\x1b[20l":         "disable newline mode",
		"\x1b[5;10r":       "scrolling region from row 5 to row 10",
		"\x1b[r":           "reset the scrolling region to the whole screen",
		"\x1b[10@":         "insert 10 blank characters",
		"\x1b[3M":          "delete 3 lines",
		"\x1b(0":           "designate G0 as DEC special graphics (line drawing)",
		"\x1b(B":           "designate G0 as US ASCII",
		"\x0e":             "shift out",
		"\x0f":             "shift in",
		"\x1b[?9999$p":     "request the state of private mode 9999",
		"\x1b[?5;6~":       "CSI sequence (prefix \"?\", final \"~\")",
		"\x1b[ ~":          "CSI sequence (intermediate \" \", final \"~\")",
		"\x1b[2~":          "CSI sequence (final \"~\")",
		"\x1b$a":           "ESC sequence (intermediate \"$\", final \"a\")",
		"\x1b[6n":          "cursor position request",
		"\x1b[21t":         "window operation: report the window title, as typed input",
		"\x1b[c":           "device attributes request",
		"\x1b[>c":          "secondary device attributes request",
		"\x1bD":            "index",
		"\x1b\\":           "string terminator",
	} {
		if got := meaningOf(t, input); !strings.Contains(got, want) {
			t.Errorf("%q: %q, want it to say %q", input, got, want)
		}
	}
}
