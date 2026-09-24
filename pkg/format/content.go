package format

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// PlainText reports whether raw is text rather than binary: valid UTF-8
// holding no NUL and no C0 control but the ones text is written with — tab,
// line feed, vertical tab, form feed, carriage return, and the escape that
// colours a log. Bytes somebody else wrote — a decoded value, a response
// body, an object in a bucket — are shown as they are when this says so, and
// through Dump when it does not.
//
// Printed as they came, bytes that are not text show as nothing. Every
// renderer strips control characters on the way to a terminal, so an escape
// sequence in the data cannot act there, and so `rta codec b64 --decode
// AAECAwT/` printed an empty line and a favicon fetched with `rta http get`
// printed a few stray letters. This and Dump lived under builtin/internal
// while only built-ins needed them, which left a plugin returning an object's
// content with the same problem and no way to import the answer — the same
// reason the rest of this package is in pkg.
//
// It answers "text or binary", the question net/http's content sniffing asks
// of the same bytes, and not "will every byte be seen". It used to be the
// second: one invisible character anywhere, a directional mark in a Hebrew
// page, the byte order mark a .NET server puts before its JSON, a form feed
// in an RFC, and the whole body was a hex dump — while the renderer, which
// leaves those marks in place, spells out the characters that reorder text
// and drops the controls, would have shown the text. What a renderer does
// with the characters it is given is the renderer's job. A caller whose
// point is showing every byte, a carriage return included, asks that itself.
func PlainText(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	// By byte: a C0 control is one byte in UTF-8, and no byte of a longer
	// character is below 0x80.
	for _, c := range raw {
		if c < 0x20 && !textControl(c) {
			return false
		}
	}
	return true
}

// textControl reports whether c is one of the C0 controls text is written
// with, as opposed to one that only turns up in binary data.
func textControl(c byte) bool {
	switch c {
	case '\t', '\n', '\v', '\f', '\r', 0x1b:
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
