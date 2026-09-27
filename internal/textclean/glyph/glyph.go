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
// nothing (internal/grant's onlyDots) — nor one it draws as an empty cell.
//
// That last pair is named by code point because no property holds them.
// U+2800, the Braille pattern with no dots raised, and U+1D159, the musical
// null notehead, are symbols to Unicode, neither space nor ignorable, and
// every font draws them as a blank: "prod/db" followed by either one read
// as the bare record, the same way a no-break space did. The Braille blank
// is the one people reach for when a name has to look empty and not be.
func Seen(r rune) bool {
	return strconv.IsPrint(r) && r != 0x2800 && r != 0x1d159 &&
		!unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) &&
		!unicode.Is(unicode.Variation_Selector, r)
}
