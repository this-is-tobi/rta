package debug

import (
	"fmt"
	"strings"
)

// The OSC commands a program leaves in a log or a terminal beside the three
// explainOSC decodes: the ones that ask the terminal a question, which it
// answers into the program's input as if the keys were typed, and the ones
// that tell a shell, a prompt or a desktop something.
//
// **An OSC was named only when it could be read back.** `printf
// '\e]11;?\a'` printed "OSC 11", a number, where it is the question "what is
// your background colour" and the terminal's answer, `rgb:1e1e/1e1e/1e1e`,
// arrives on the standard input of whatever is running: the reply that
// appears, after a program that queried the terminal and exited, as junk on
// the command line. A CSI of the same class is named for that reason (csi.go),
// and the OSC with it. A command nothing here names keeps the number it had.

// dynamicColors are OSC 10 to 12, the colours a program may set or ask for,
// and the resets (110 to 112) that take them back.
var dynamicColors = map[int]string{10: "foreground colour", 11: "background colour", 12: "cursor colour"}

// namedOSC explains command cmd with payload, or reports that nothing here
// names it.
func namedOSC(cmd int, payload string) (string, bool) {
	switch cmd {
	case 4:
		return palette(payload), true
	case 7:
		return "set the working directory: " + visualize(payload), true
	case 9:
		return notification(payload), true
	case 10, 11, 12:
		return dynamicColor(dynamicColors[cmd], payload), true
	case 99:
		return "desktop notification (kitty): " + visualize(payload), true
	case 104:
		return "reset the palette colours", true
	case 110, 111, 112:
		return "reset the " + dynamicColors[cmd-100], true
	case 133:
		return "shell integration mark: " + visualize(payload), true
	case 777:
		return "desktop notification (rxvt): " + visualize(strings.TrimPrefix(payload, "notify;")), true
	case 1337:
		return iterm(payload), true
	}
	return "", false
}

// asksTerminal reports whether an OSC payload is the "?" that turns a setting
// into a question the terminal answers.
func asksTerminal(value string) bool { return value == "?" }

func dynamicColor(name, value string) string {
	if asksTerminal(value) {
		return "report the " + name + " — the terminal answers into the input, as if typed"
	}
	return "set the " + name + " to " + visualize(value)
}

// palette is OSC 4's "index;colour" pairs, of which a question is the ones a
// terminal answers.
func palette(payload string) string {
	parts := strings.Split(payload, ";")
	var asked, set []string
	for i := 0; i+1 < len(parts); i += 2 {
		if asksTerminal(parts[i+1]) {
			asked = append(asked, parts[i])
		} else {
			set = append(set, parts[i])
		}
	}
	var out []string
	if len(set) > 0 {
		out = append(out, fmt.Sprintf("set palette colour %s", visualize(strings.Join(set, ", "))))
	}
	if len(asked) > 0 {
		out = append(out, fmt.Sprintf("report palette colour %s — the terminal answers into the input, as if typed",
			visualize(strings.Join(asked, ", "))))
	}
	if len(out) == 0 {
		return "change the palette"
	}
	return strings.Join(out, "; ")
}

// notification is OSC 9, which iTerm2 reads as a desktop notification and
// ConEmu and Windows Terminal read as a progress report when it opens with 4.
func notification(payload string) string {
	if rest, ok := strings.CutPrefix(payload, "4;"); ok {
		return "progress report (ConEmu, Windows Terminal): " + visualize(rest)
	}
	return "desktop notification (iTerm2): " + visualize(payload)
}

// iterm is OSC 1337, iTerm2's own commands, among them the transfer of a file
// to the terminal and the setting of a variable the user's shell reads back.
func iterm(payload string) string {
	switch {
	case strings.HasPrefix(payload, "File="):
		return "iTerm2 inline file — a file the terminal saves or draws"
	case strings.HasPrefix(payload, "SetUserVar="):
		return "iTerm2 user variable — a value the terminal keeps for its status bar and triggers"
	}
	return "iTerm2 proprietary command"
}
