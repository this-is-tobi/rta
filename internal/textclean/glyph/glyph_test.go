package glyph

import (
	"strconv"
	"testing"
	"unicode"
)

// Every class the rule names, by a sample of each, is a character a reader
// does not see as itself: what strconv.IsPrint leaves out — a control, a
// format character, a space other than the ASCII one, a separator, a private
// or an unassigned code point — and, among what it counts as printable, a
// default-ignorable code point, a variation selector, and the blanks named by
// code point. Each fixture is built from its code point, since the
// characters themselves are what no reviewer could see in this file.
func TestACharacterAReaderDoesNotSeeIsNotSeen(t *testing.T) {
	for class, runes := range map[string][]rune{
		"a C0 control, DEL and a C1 control": {0x00, 0x07, 0x09, 0x0a, 0x0d, 0x1b, 0x1f, 0x7f, 0x80, 0x85, 0x9b, 0x9f},
		"a format character": {
			0xad, 0x061c, 0x180e, 0x200b, 0x200c, 0x200d, 0x200e, 0x200f, 0x202a, 0x202e, 0x2060,
			0x2064, 0x2066, 0x2069, 0xfeff, 0x1bca0, 0x1d173, 0xe0001, 0xe0041, 0xe007f,
		},
		"a space other than the ASCII one":       {0xa0, 0x1680, 0x2000, 0x2007, 0x200a, 0x202f, 0x205f, 0x3000},
		"a line or paragraph separator":          {0x2028, 0x2029},
		"a private-use code point":               {0xe000, 0xf8ff, 0xf0000, 0x10fffd},
		"an unassigned code point":               {0x0378, 0x0379, 0xfff0},
		"a noncharacter":                         {0xfdd0, 0xfffe, 0xffff, 0x2fffe, 0x10ffff},
		"a surrogate half, which no UTF-8 holds": {0xd800, 0xdfff},
		"a default-ignorable code point Unicode counts printable": {
			0x034f, 0x115f, 0x1160, 0x17b4, 0x17b5, 0x3164, 0xffa0,
		},
		"a variation selector":            {0x180b, 0x180d, 0x180f, 0xfe00, 0xfe0f, 0xe0100, 0xe01ef},
		"a blank named by its code point": {0x2800, 0x1d159},
	} {
		for _, r := range runes {
			if Seen(r) {
				t.Errorf("%s, U+%04X, is counted as seen", class, r)
			}
		}
	}
}

// Every character of the two properties the rule reads is one it counts as
// not seen, the whole of each table rather than a sample, so a character a
// later Unicode adds to either is covered the day Go's tables carry it.
func TestEveryDefaultIgnorableAndVariationSelectorIsNotSeen(t *testing.T) {
	for name, table := range map[string]*unicode.RangeTable{
		"Other_Default_Ignorable_Code_Point": unicode.Other_Default_Ignorable_Code_Point,
		"Variation_Selector":                 unicode.Variation_Selector,
	} {
		n := 0
		each(table, func(r rune) {
			n++
			if Seen(r) {
				t.Errorf("U+%04X, in %s, is counted as seen", r, name)
			}
		})
		if n == 0 {
			t.Errorf("%s holds nothing, so this checks nothing", name)
		}
	}
}

// The blanks are named by code point because no property holds them: each is
// a character strconv.IsPrint counts printable, in neither table the rule
// reads. One a later Unicode moves into either would be caught by the rule
// twice, and this says so, so the list stays the few no property covers.
func TestTheBlanksNamedByCodePointAreOnesNoPropertyHolds(t *testing.T) {
	for _, r := range []rune{0x2800, 0x1d159} {
		if !strconv.IsPrint(r) || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, r) ||
			unicode.Is(unicode.Variation_Selector, r) {
			t.Errorf("U+%04X is held by a property the rule already reads; name it no longer", r)
		}
	}
}

// What a reader sees as itself stays seen, however far from ASCII: the
// letters, digits, punctuation and symbols of ASCII and the space, a letter
// with an accent precomposed or written with a combining mark, a script
// other than Latin, an ideograph, a Hangul syllable and a jamo that is not a
// filler, a Braille pattern with a dot raised, a musical symbol that draws,
// an emoji, and the replacement character a renderer draws for a byte that
// is not UTF-8.
func TestACharacterAReaderSeesIsSeen(t *testing.T) {
	var runes []rune
	for r := rune(0x20); r < 0x7f; r++ {
		runes = append(runes, r)
	}
	runes = append(runes,
		0xe9, 0x0301, 0x00f1, 0x0416, 0x05d0, 0x0627, 0x0915, 0x0e01, 0x6771, 0x4eac, 0xac00,
		0x1100, 0x1161, 0x2801, 0x28ff, 0x1d15a, 0x1f600, 0x2764, 0x20ac, 0x2026, 0x2014, 0xfffd,
	)
	for _, r := range runes {
		if !Seen(r) {
			t.Errorf("U+%04X is counted as not seen", r)
		}
	}
}

// each calls f with every code point in table.
func each(table *unicode.RangeTable, f func(rune)) {
	for _, r16 := range table.R16 {
		for r := rune(r16.Lo); r <= rune(r16.Hi); r += rune(r16.Stride) {
			f(r)
		}
	}
	for _, r32 := range table.R32 {
		for r := rune(r32.Lo); r <= rune(r32.Hi); r += rune(r32.Stride) {
			f(r)
		}
	}
}
