package format

import (
	"strconv"
	"strings"
)

// Plural is one of two words, chosen by a count it does not print:
// Plural(len(rows), "is", "are"), where the number is either already on the
// line or deliberately not on it.
//
// **Two functions rather than one with a flag, because one name for both is
// what went wrong.** Six packages had each grown a local
// `plural(n int, one, many string) string`, and they were not the same
// function: four returned the word alone, two returned the count with it.
// Identical name, identical signature, different output — so a call written
// from memory, or a line carried from one package to another, silently
// produced "3 3 days" or dropped the number, and the compiler had nothing to
// say about it. Here they have names that cannot be confused, in the package
// whose whole purpose is that everybody says the same thing.
//
// Only exactly one is singular. Zero is plural in English ("0 files"), and a
// negative count is a bug further up that still has to render rather than
// panic; both were already true of every copy this replaces, and both are
// easy to "correct" into something worse.
func Plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Count is the number and its word: Count(len(rows), "day", "days") is
// "3 days". Plural's sibling for the lines that carry the figure.
func Count(n int, one, many string) string {
	return strconv.Itoa(n) + " " + Plural(n, one, many)
}

// PluralOf is a noun in the form a count calls for, derived rather than
// given: PluralOf(len(hits), "advisory") is "advisories".
//
// English is not worth modelling and this does not try. The -y rule is the
// one that comes up — advisory/advisories, capability/capabilities — and a
// consonant before the y is the whole of it, so "key" and "day" keep theirs.
// Getting that backwards would trade one wrong plural for another in a
// codebase that counts keys. A noun too short to have a letter before its y
// is left alone for the same reason the rule is narrow: the alternative is
// indexing off the front of a string.
//
// Three packages had this written out and a fourth had it written wrong —
// the TUI's derived form was a bare "+s", so the same noun read "entries"
// from `kv` and "entrys" from a form box. A botched plural on a security
// report reads as carelessness about everything else in it.
func PluralOf(n int, noun string) string {
	if n == 1 {
		return noun
	}
	if len(noun) > 1 && strings.HasSuffix(noun, "y") &&
		!strings.ContainsRune("aeiou", rune(noun[len(noun)-2])) {
		return noun[:len(noun)-1] + "ies"
	}
	// A sibilant takes -es. The second rule that comes up in practice, and
	// the louder one: a bare +s on "entry" reads as a typo, while a bare +s
	// on "process" or "status" produces a triple letter that is not a word
	// at all. Found by using it — `sys ps` counted what it could not read
	// and printed the mangled noun on its first run.
	for _, end := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(noun, end) {
			return noun + "es"
		}
	}
	return noun + "s"
}

// CountOf is the number and its noun, derived: CountOf(2, "entry") is
// "2 entries". PluralOf's sibling, the way Count is Plural's.
func CountOf(n int, noun string) string {
	return strconv.Itoa(n) + " " + PluralOf(n, noun)
}
