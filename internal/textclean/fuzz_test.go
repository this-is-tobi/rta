package textclean

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean/glyph"
)

// What the two cleaners promise, held against arbitrary bytes: nothing a
// terminal acts on survives Terminal, nothing a model reads as invisible
// survives Model, and cleaning twice is cleaning once. Every string an agent
// ever reads back from rta went through one of these, and this is the code
// path where a missed byte is a security event rather than a display bug.
//
// Cleaning twice is cleaning once for Terminal too, and not only because the
// TUI and the renderer can each clean one value: lockdown holds a credential
// lock's name to what Terminal makes of it, and the name is one the bridge
// already cleaned, so a second pass that changed it would leave the identity
// an incident is about impossible to lock.
func FuzzTerminal(f *testing.F) {
	for _, seed := range []string{
		"plain", "tab\tand\nnewline", "esc\x1b[31mred", "osc\x1b]52;c;Y3VybA==\x07",
		"c1 csi\x9b2J", "c1 osc\x9d0;title\x9c", "del\x7f", "bad\xff\xe2\x80\xaeutf8 beside an override",
		"bidi\u202e", "\xff\xfe bad utf8", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Terminal(s)
		if strings.ContainsFunc(out, actsOn) || !utf8.ValidString(out) {
			t.Fatalf("Terminal(%q) = %q still carries a character or a byte a terminal acts on", s, out)
		}
		if again := Terminal(out); again != out {
			t.Fatalf("Terminal is not idempotent: %q -> %q -> %q", s, out, again)
		}
		if !dirtyForTerminal(s) && out != s {
			t.Fatalf("Terminal changed a clean string: %q -> %q", s, out)
		}
	})
}

func FuzzModel(f *testing.F) {
	for _, seed := range []string{
		"plain", "zero\u200bwidth", "tag\U000e0041block", "bidi\u202eoverride", "joiner\u2060",
		"esc\x1b[0m and \u200b both", "", "\xff",
		// Invalid UTF-8 that ansi.Strip turns into U+E0001 by dropping the
		// bytes between its pieces — which is why the invisible filter runs
		// after the strip and not before.
		"\xf3\xf3\xa0\x80\xf7\x81\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Model(s)
		if strings.ContainsFunc(out, actsOn) || strings.ContainsFunc(out, isInvisible) || !utf8.ValidString(out) {
			t.Fatalf("Model(%q) = %q still carries something a model reads and a person cannot see", s, out)
		}
		if again := Model(out); again != out {
			t.Fatalf("Model is not idempotent: %q -> %q -> %q", s, out, again)
		}
	})
}

// What Record promises a person approving a record, held against arbitrary
// bytes: shown as it is only when every character reads as itself, quoted
// otherwise in a form that reads back as exactly the record, and never the
// same showing for two records — shown as it is never begins with a
// quotation mark, quoted always does, and Go's quoting is one to one. What
// it shows holds nothing a terminal acts on and nothing a reader cannot see.
func FuzzRecord(f *testing.F) {
	for _, seed := range []string{
		"prod/db", "prod/db ", " prod/db", "two words", `"quoted"`, `C:\Users\me`, "", "\xff",
		"prod/db" + string(rune(0xa0)), "prod/db" + string(rune(0x200b)), "a" + string(rune(0xfe0f)),
		"prod/db" + string(rune(0x2800)),
		"invoice" + string(rune(0x202e)) + "fdp.exe", "tag" + string(rune(0xe0041)), "tab\tnew\nline",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Record(s)
		if strings.ContainsFunc(out, func(r rune) bool { return !glyph.Seen(r) }) || !utf8.ValidString(out) {
			t.Fatalf("Record(%q) = %q still holds a character a reader does not see as itself", s, out)
		}
		if Terminal(out) != out {
			t.Fatalf("Record(%q) = %q holds something a terminal acts on", s, out)
		}
		if out == s {
			if strings.HasPrefix(s, `"`) || strings.ContainsRune(s, ' ') {
				t.Fatalf("Record(%q) showed it as it is, and it reads as a quoted or a listed record", s)
			}
			return
		}
		if back, err := strconv.Unquote(out); err != nil || back != s {
			t.Fatalf("Record(%q) = %q, which reads back as %q (%v)", s, out, back, err)
		}
	})
}
