package textclean

import (
	"strings"
	"testing"
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
		"c1 csi\x9b2J", "del\x7f", "bad\xff\xe2\x80\xaeutf8 beside an override",
		"bidi\u202e", "\xff\xfe bad utf8", "",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Terminal(s)
		if strings.ContainsFunc(out, actsOn) {
			t.Fatalf("Terminal(%q) = %q still carries a character a terminal acts on", s, out)
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
		if strings.ContainsFunc(out, actsOn) || strings.ContainsFunc(out, isInvisible) {
			t.Fatalf("Model(%q) = %q still carries something a model reads and a person cannot see", s, out)
		}
		if again := Model(out); again != out {
			t.Fatalf("Model is not idempotent: %q -> %q -> %q", s, out, again)
		}
	})
}
