// Package glyph is textclean's rule for which characters a reader sees as
// themselves, for the places that have to agree about it: textclean.Record,
// which quotes a record holding one a reader would not see, shellquote.Arg,
// which spells one by its bytes in a command a person is shown and pastes,
// and the TUI's spelling of a call (plugin.Surface.Call), which quotes a
// box's value holding one.
//
// A package of its own because they could not share the rule otherwise:
// textclean reads pkg/plugin, and pkg/plugin quotes the commands its hints
// name with shellquote, so pkg/plugin or shellquote reading textclean would
// be a cycle. A copy in each is how they came to disagree. shellquote asked
// unicode.IsPrint alone, so a record ending in a Hangul filler was shown
// quoted, its filler named, on the refusal that handed on a grant command
// with the same record in plain single quotes — reading as the bare record,
// which is not the one the command grants.
package glyph

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Seen reports whether a reader sees r as itself: a letter, a mark, a
// number, a punctuation mark, a symbol or the ASCII space (strconv.IsPrint),
// and not one a renderer draws as nothing — a default-ignorable code point
// or a variation selector, the classes the grant matcher already reads as
// nothing (internal/grant's onlyDots) — nor one it draws as an empty cell,
// nor the one mark the fonts that cover it draw as nothing.
//
// Those last three are named by code point because no property holds them.
// U+2800, the Braille pattern with no dots raised, and U+1D159, the musical
// null notehead, are symbols to Unicode, neither space nor ignorable, and
// every font draws them as a blank: "prod/db" followed by either one read
// as the bare record, the same way a no-break space did. The Braille blank
// is the one people reach for when a name has to look empty and not be.
//
// U+16FE4, the Khitan Small Script filler, is a mark to Unicode and not a
// default-ignorable one, so strconv counts it printable, but it only says
// how a cluster of Khitan characters is laid out, and the fonts that cover
// it — Noto's three Khitan Small Script faces — draw it as nothing, so
// "prod/db" and the filler read as "prod/db" there. Where no font covers it
// a renderer draws its box for a missing glyph, which a reader does see;
// counted unseen, it is quoted and named on every screen, which costs
// nothing where the box would have shown it anyway.
func Seen(r rune) bool {
	return strconv.IsPrint(r) && r != 0x2800 && r != 0x1d159 && r != 0x16fe4 &&
		!unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) &&
		!unicode.Is(unicode.Variation_Selector, r)
}

// Name is a name read off a disk, a bucket or a store as a list of them
// shows it: as it is when it reads as itself, spaces and accents included, and
// Quote otherwise. One with a space at either end, which draws as nothing, or
// opening with a quotation mark is quoted too, so a name shown as it is can
// never be mistaken for one shown quoted. textclean.Name and the plugin SDK's
// ListedName are this, in the one place so that they cannot disagree.
func Name(s string) string {
	if s != "" && s[0] != '"' && utf8.ValidString(s) && strings.TrimSpace(s) == s &&
		!strings.ContainsFunc(s, func(r rune) bool { return r != ' ' && !Seen(r) }) {
		return s
	}
	return Quote(s)
}

// Quote is s in double quotes, as Go quotes a string, with every character a
// reader would not see as itself (Seen) named by its code point and every
// byte that is not UTF-8 by its value: the one spelling from which a person
// can tell two values apart that would draw alike. The ASCII space is kept
// as it is, since inside the quotes it can be seen. It is how
// textclean.Record shows a record that does not read as itself, and how the
// TUI's spelling of a call quotes a box's value.
//
// strconv.Quote asks strconv.IsPrint, which counts a Hangul filler, a
// Braille blank and a variation selector printable and leaves them raw, so
// a value ending in one read, quoted, as the value without it.
func Quote(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	b.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteByte(s[i])
		case r == ' ' || Seen(r):
			b.WriteString(s[i : i+size])
		default:
			// Go's own escape for it: a letter for the controls that have
			// one, the code point for everything else.
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += size
	}
	b.WriteByte('"')
	return b.String()
}
