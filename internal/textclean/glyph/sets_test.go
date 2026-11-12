package glyph

import "testing"

// The two sets a declaration refuses and a result loses, by code point: in,
// and, on either side and among what is deliberately left out, not.
func TestTheSetsADeclarationRefusesAndAResultLoses(t *testing.T) {
	for name, tc := range map[string]struct {
		in   func(rune) bool
		yes  []rune
		none []rune
	}{
		"reorders": {Reorders,
			[]rune{0x202a, 0x202b, 0x202c, 0x202d, 0x202e, 0x2066, 0x2067, 0x2068, 0x2069},
			[]rune{0x2029, 0x202f, 0x2065, 0x206a, 0x200e, 0x200f, 'a'}},
		"hidden": {Hidden,
			[]rune{0x200b, 0x200e, 0x200f, 0xfeff, 0x2060, 0x2061, 0x2062, 0x2063, 0x2064, 0xe0000, 0xe0041, 0xe007f},
			[]rune{0x200c, 0x200d, 0xfe0f, 0xfe00, 0xe0100, 0x2065, 0x202e, 0xdffff, 0xe0080, 'a', ' '}},
	} {
		for _, r := range tc.yes {
			if !tc.in(r) {
				t.Errorf("%s(%#U) = false", name, r)
			}
		}
		for _, r := range tc.none {
			if tc.in(r) {
				t.Errorf("%s(%#U) = true", name, r)
			}
		}
	}
}
