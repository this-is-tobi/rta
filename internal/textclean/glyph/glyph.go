// Package glyph is textclean's rule for which characters a reader sees as
// themselves, for the places that have to agree about it: textclean.Record,
// which quotes a record holding one a reader would not see, and
// shellquote.Arg, which spells one by its bytes in a command a person is
// shown and pastes.
//
// A package of its own because the two could not share the rule otherwise:
// textclean reads pkg/plugin, and pkg/plugin quotes the commands its hints
// name with shellquote, so shellquote reading textclean would be a cycle. A
// copy in each is how they came to disagree. shellquote asked unicode.IsPrint
// alone, so a record ending in a Hangul filler was shown quoted, its filler
// named, on the refusal that handed on a grant command with the same record
// in plain single quotes — reading as the bare record, which is not the one
// the command grants.
package glyph

import (
	"strconv"
	"unicode"
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
