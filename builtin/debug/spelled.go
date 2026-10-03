package debug

import (
	"slices"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/this-is-tobi/rta/pkg/view"
)

// escapeSpellings are the ways the escape character is written out where the
// character itself would have been acted on: `\033` and `\e` in a shell
// script, `\x1b` in Go's %q and Python's repr, `\u001b` in a JSON log line,
// `^[` in cat -v and less. None is the start of another.
var escapeSpellings = []string{`\033`, `\x1b`, `\x1B`, `\u001b`, `\u001B`, `\e`, `\E`, "^["}

// bellSpellings are the same for the bell, which ends the window-title
// sequence a shell prompt prints before every line: `^G` in cat -v, and
// `\007` or `\u0007` in a script or a JSON line. `\a` is left out, being as
// likely the start of a file name as a bell.
var bellSpellings = []string{`\007`, `\x07`, `\u0007`, "^G"}

// unspell reads an escape character written out as text as the character.
// found lists the spellings it read, in the order they first appear, and is
// empty when it changed nothing.
//
// **A log shows an escape sequence spelled out, and the text was explained as
// text.** `rta debug ansi '\033[1mhi'`, `'\x1b[31m'` and `'^[[0m'` — what a
// captured log, `cat -v` and a JSON line print — came back as one row of
// printable text and a dash, from a command whose job is to say what the
// sequence does. The shell hands over a backslash and a zero for `'\033'`, not
// the character, so the spelling is all that ever reaches this.
//
// Only a spelling followed by `[` or `]` is read, the introducers of the
// control and operating-system sequences a log carries, so that a backslash
// and an e in a path or a regular expression stay text; and only in a text
// that holds no escape character itself, which is the one that means what it
// says. Inside a window-title sequence opened that way the bell that ends it
// is read too, since it is the one every shell prompt leaves in a log. The
// caller is told what was read, in the table, since the rows are then an
// explanation of text that is not quite what was given.
func unspell(s string) (decoded string, found []string) {
	if strings.IndexByte(s, ansi.ESC) >= 0 {
		return s, nil
	}
	var b strings.Builder
	note := func(spelling string) {
		if !slices.Contains(found, spelling) {
			found = append(found, spelling)
		}
	}
	inOSC := false
scan:
	for i := 0; i < len(s); {
		for _, sp := range escapeSpellings {
			if strings.HasPrefix(s[i:], sp) && i+len(sp) < len(s) && (s[i+len(sp)] == '[' || s[i+len(sp)] == ']') {
				b.WriteByte(ansi.ESC)
				inOSC = s[i+len(sp)] == ']'
				i += len(sp)
				note(sp)
				continue scan
			}
		}
		if inOSC {
			for _, sp := range bellSpellings {
				if strings.HasPrefix(s[i:], sp) {
					b.WriteByte(ansi.BEL)
					inOSC = false
					i += len(sp)
					note(sp)
					continue scan
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	if len(found) == 0 {
		return s, nil
	}
	return b.String(), found
}

// spelledWarning says an escape character was read out of its spelling.
func spelledWarning(found []string) view.Error {
	return view.Error{
		Code: "debug.ansi.spelled",
		Message: "the text spells the escape character out (" + strings.Join(found, ", ") +
			") instead of holding it, and is explained as if it held it",
		Hint: "a shell hands over the character itself for $'\\033[1m', and printf '\\033[1m' | rta debug ansi gives it exactly",
	}
}
