package codec

import (
	"errors"
	"fmt"
	"strings"

	"github.com/this-is-tobi/rta/pkg/format"
)

// b64Failure is why value, with its whitespace already gone, is not base64 in
// any of the four dialects decodeB64 tries, in words about the value.
//
// **The reason was the last dialect's, at an offset into a string it was not
// about.** decodeB64 tries the standard and URL-safe alphabets, padded and
// not, and the error it returned was the final attempt's: "illegal base64 data
// at input byte 7" for `aGVsbG8==`, where the byte is the first of two padding
// characters that the unpadded URL-safe decoder, which has no use for any,
// stopped at; and byte 0 for a lone `a`, which fails for its length, and for
// `@@@`, which fails for its characters, both in the same words. The reasons
// are told apart here, each by what the reader would change: a character
// outside every alphabet, the two alphabets mixed in one value, padding that
// is not at the end or is too long, or a length no whole bytes come out of.
func b64Failure(value string, fallback error) error {
	standard, urlSafe := strings.ContainsAny(value, "+/"), strings.ContainsAny(value, "-_")
	for i, r := range []rune(value) {
		if !isB64Rune(r) {
			return fmt.Errorf("character %d, %q, is not one base64 uses", i+1, string(r))
		}
	}
	if standard && urlSafe {
		return errors.New("it mixes the standard alphabet (+ and /) with the URL-safe one (- and _), which no encoder writes in one value")
	}
	data := strings.TrimRight(value, "=")
	padding := len(value) - len(data)
	switch {
	case strings.Contains(data, "="):
		return errors.New("a padding = sits inside the value, where it can only end it")
	case padding > 2:
		return fmt.Errorf("it ends in %s, where at most two pad a value", format.CountOf(padding, "padding character"))
	case len(data)%4 == 1:
		return errors.New("its last group of four holds a single character, which carries less than a byte")
	case padding > 0 && (len(data)+padding)%4 != 0:
		return fmt.Errorf("%s of data and %s do not make whole groups of four",
			format.CountOf(len(data), "character"), format.CountOf(padding, "padding character"))
	}
	return fallback
}

func isB64Rune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	}
	return strings.ContainsRune("+/-_=", r)
}
