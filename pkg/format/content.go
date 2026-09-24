package format

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PlainText reports whether raw is text a terminal shows exactly as it is:
// valid UTF-8 holding nothing a renderer strips or a reader cannot see, the
// line breaks and tabs of ordinary text aside. Bytes somebody else wrote — a
// decoded value, a response body, an object in a bucket — are shown as they
// are when this says so, and through Dump when it does not.
//
// Printed as they came, bytes that are not text show as nothing. Every
// renderer strips control characters on the way to a terminal, so an escape
// sequence in the data cannot act there, and so `rta codec b64 --decode
// AAECAwT/` printed an empty line and a favicon fetched with `rta http get`
// printed a few stray letters. This and Dump lived under builtin/internal
// while only built-ins needed them, which left a plugin returning an object's
// content with the same problem and no way to import the answer — the same
// reason the rest of this package is in pkg.
func PlainText(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for _, r := range string(raw) {
		if hidden(r) {
			return false
		}
	}
	return true
}

// hidden is the renderer's rule for what does not display as itself, one
// character at a time: a control character a terminal would act on, and the
// invisible and bidi characters that hide or reorder what a reader sees.
//
// Written out here rather than asked of the renderer's own cleaner because
// this package is imported by every plugin, and that cleaner brings an ANSI
// parser along that no plugin binary should pay for to answer a question about
// one rune. internal/textclean holds the two to the same answer for every
// character there is, so they cannot drift apart unnoticed.
func hidden(r rune) bool {
	switch {
	case r == '\n', r == '\t', r == '\r':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x200b, r == 0x200e, r == 0x200f, r == 0xfeff,
		r >= 0x202a && r <= 0x202e,
		r >= 0x2060 && r <= 0x2064,
		r >= 0x2066 && r <= 0x2069,
		r >= 0xe0000 && r <= 0xe007f:
		return true
	}
	return false
}

// Dump renders raw in the layout `hexdump -C` made familiar — offset, sixteen
// bytes in hex, the printable ones beside them — showing at most limit
// bytes, and saying so when that is fewer than there are.
func Dump(raw []byte, limit int) string {
	limit = max(limit, 0)
	shown := raw
	var b strings.Builder
	b.WriteString(CountOf(len(raw), "byte") + ", not plain text")
	if len(raw) > limit {
		shown = raw[:limit]
		b.WriteString(" — the first " + CountOf(limit, "byte") + " shown")
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
	cut := max(limit, 0)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "\n… (" + CountOf(len(text)-cut, "more byte") + ")"
}
