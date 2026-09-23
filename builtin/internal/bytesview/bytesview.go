// Package bytesview shows bytes a built-in did not write: a decoded value, a
// response body, a token's payload — as text when they are text, and as a
// dump of every byte when they are not.
//
// Printed as they came, bytes that are not text show as nothing. Every
// renderer strips control characters on the way to a terminal, so an escape
// sequence in the data cannot act there, and so `rta codec b64 --decode
// AAECAwT/` printed an empty line and a favicon fetched with `rta http get`
// printed a few stray letters. Deciding which a value is, and dumping it, was
// about to be written a second time; this is the one copy.
package bytesview

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/this-is-tobi/rta/internal/textclean"
	"github.com/this-is-tobi/rta/pkg/format"
)

// PlainText reports whether raw is text a terminal shows exactly as it is:
// valid UTF-8 holding nothing a renderer strips or a reader cannot see, the
// line breaks and tabs of ordinary text aside.
func PlainText(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for _, r := range string(raw) {
		if r != '\n' && r != '\t' && r != '\r' && textclean.Deceives(string(r)) {
			return false
		}
	}
	return true
}

// Dump renders raw in the layout `hexdump -C` made familiar — offset, sixteen
// bytes in hex, the printable ones beside them — showing at most limit
// bytes, and saying so when that is fewer than there are.
func Dump(raw []byte, limit int) string {
	shown := raw
	var b strings.Builder
	b.WriteString(format.CountOf(len(raw), "byte") + ", not plain text")
	if len(raw) > limit {
		shown = raw[:limit]
		b.WriteString(" — the first " + format.CountOf(limit, "byte") + " shown")
	}
	b.WriteString(":")
	for off := 0; off < len(shown); off += 16 {
		line := shown[off:min(off+16, len(shown))]
		fmt.Fprintf(&b, "\n%08x  ", off)
		for i := range 16 {
			if i < len(line) {
				fmt.Fprintf(&b, "%02x ", line[i])
			} else {
				b.WriteString("   ")
			}
			if i == 7 {
				b.WriteByte(' ')
			}
		}
		b.WriteString(" |")
		for _, c := range line {
			if c >= 0x20 && c < 0x7f {
				b.WriteByte(c)
			} else {
				b.WriteByte('.')
			}
		}
		b.WriteString("|")
	}
	return b.String()
}

// Truncate cuts text to at most limit bytes without splitting a character,
// and says how much it left out. A cut at a byte offset can end halfway
// through a multi-byte character, which is no longer valid UTF-8 and renders
// as a replacement glyph that was never in the data.
func Truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n… (" + format.CountOf(len(text)-cut, "more byte") + ")"
}
