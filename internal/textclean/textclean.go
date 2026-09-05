// Package textclean neutralises text on its way to a consumer that will act
// on it.
//
// Two consumers, two different ideas of what "act on it" means, one shared
// idea of where the text came from. A view carries data from somewhere else —
// an HTTP response body, a DNS record, a certificate subject, a filename, a
// database row — so the rule that "views carry data, never ANSI codes" was one
// the code we write kept and the data we display never did.
//
// Terminal is for a person reading a screen. Model is for an LLM reading tool
// output, which is a superset: an MCP client usually renders that output in a
// terminal too, so everything Terminal removes has to go, plus the characters
// that are invisible to the reviewer and not to the reader.
package textclean

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// Terminal removes what a terminal would act on.
//
// ansi.Strip runs first so a sequence goes as a unit — OSC 52's terminating
// BEL belongs to the sequence, and dropping stray control bytes first would
// leave the introducer behind for the terminal to resynchronise on.
//
// Newline and tab survive: both are content a value may legitimately carry,
// both are handled by the layout, and neither moves the cursor anywhere a
// reader cannot see. Everything else in C0, DEL, and C1 (0x9B is CSI in its
// 8-bit form, which ansi.Strip does not treat as an introducer) is dropped
// rather than escaped, because a visible marker in every cell of a hexdump is
// noise and the value is being shown to be read, not round-tripped — `-o json`
// is where round-tripping lives.
func Terminal(s string) string {
	if !dirtyForTerminal(s) {
		return s
	}
	return filter(ansi.Strip(s), isTerminalControl)
}

// Model removes what a model would read as instructions.
//
// It is Terminal plus two things a terminal does not care about and a model
// does. Invisible characters — the tag block above all, which is a full
// invisible ASCII alphabet — mean the text a person reviews and the text a
// model reads are different documents. And the authorship frame is how a
// published tool description says which half rta wrote, so a *result* able to
// contain that literal could close the frame from inside the data and continue
// in rta's voice; the declaration is already refused it at registration, and
// results are not declared at all.
//
// This is not a claim to have solved prompt injection. Tool output is
// attacker-influenced text arriving in a model's context and it stays that
// way; what this removes is the part that is invisible to the human who would
// otherwise have a chance of noticing.
func Model(s string) string {
	out := Terminal(s)
	if strings.ContainsFunc(out, isInvisible) {
		out = filter(out, isInvisible)
	}
	// By sense rather than by bytes: the reader is a model, and "─── end of
	// plugin-written text ───" with a third dash closes the block as
	// convincingly as the literal while matching no ReplaceAll. See
	// plugin.StripFrames.
	return plugin.StripFrames(out)
}

// Deceives reports whether s would display as something other than what it
// is: the control and escape sequences a terminal acts on, and the invisible
// and bidi characters that hide or reorder what a reader sees.
//
// A predicate rather than a third cleaner, because its one caller must not
// clean. internal/recent remembers what an operator used so a completion can
// offer it back, and a completion entry is a string somebody accepts as an
// input with one keystroke — so handing them a *cleaned* version would hand
// them a value that is not the one that worked. Declining to remember it costs
// nothing, and leaves nothing on a list that reads as one thing and is
// another.
func Deceives(s string) bool {
	// Newline and tab included here and exempt in isTerminalControl, and the
	// difference is the context. A *view* value may legitimately hold either —
	// a note body, a table cell — and the layout handles them. A completion
	// entry is one line offered as one value: a newline in it renders as two
	// entries and a tab as a column break, so what somebody accepts is not
	// what they saw.
	return strings.ContainsAny(s, "\n\t") ||
		dirtyForTerminal(s) || strings.ContainsFunc(s, isInvisible)
}

func filter(s string, drop func(rune) bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !drop(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isTerminalControl(r rune) bool {
	if r == '\n' || r == '\t' {
		return false
	}
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// dirtyForTerminal reports whether Terminal would change s, without
// allocating. Most strings are clean and the TUI re-renders every pane on
// every keystroke.
func dirtyForTerminal(s string) bool {
	return strings.ContainsFunc(s, isTerminalControl)
}

// isInvisible mirrors the set pkg/plugin refuses in a declaration, and for the
// same reasons — including the reasons for the exclusions. U+200C ZWNJ and
// U+200D ZWJ build emoji sequences and select letter forms in Persian and
// Devanagari; variation selectors are how an emoji gets its emoji
// presentation. Dropping them from a *result* would corrupt somebody's data,
// which is a worse outcome here than in a declaration, since a result is a
// filename or a row and not a label its author can rewrite.
func isInvisible(r rune) bool {
	switch {
	case r == 0x200b, // ZERO WIDTH SPACE
		r == 0x200e, r == 0x200f, // LRM, RLM
		r == 0xfeff,                  // ZWNBSP / BOM
		r >= 0x202a && r <= 0x202e,   // the bidi overrides
		r >= 0x2060 && r <= 0x2064,   // word joiner and the invisible operators
		r >= 0x2066 && r <= 0x2069,   // the bidi isolates
		r >= 0xe0000 && r <= 0xe007f: // the tag block
		return true
	}
	return false
}
