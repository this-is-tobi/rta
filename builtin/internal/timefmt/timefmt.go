// Package timefmt reads and writes an instant, for the built-ins that have to
// do both.
//
// Three of them do, and they were each doing it alone. `agent log --since`
// accepts an exact instant in four spellings, written out at the call site;
// codec.jwt prints an `exp` claim as the raw integer the token carries, with a
// comment explaining that it is "supposed to be read as a Unix time" and no
// code that reads it as one; and nothing anywhere could answer "what is
// 1516242622" without the reader doing the arithmetic.
//
// One parser and one rendering, read by all of them, for the reason x509check
// exists: two built-ins answering the same question differently about the same
// input is the bug, and it stays invisible until somebody puts the two answers
// side by side.
package timefmt

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
)

// layouts are the exact spellings somebody types or pastes. RFC3339 is what a
// machine wrote; the rest are what a person types when they do not want to
// think about a timezone suffix, and are read in the caller's location.
//
// The zone-less "T" form is here because it is what comes out of a log line
// copied by hand — the same instant as the space-separated form, spelled the
// way the log spelled it. It widens what `agent log --since` accepts and
// narrows nothing.
var layouts = []string{
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
}

// Unit names the precision a bare number was read at. It is part of the
// answer, not a detail: see ParseEpoch.
type Unit string

const (
	Seconds Unit = "epoch seconds"
	Millis  Unit = "epoch milliseconds"
	Micros  Unit = "epoch microseconds"
	Nanos   Unit = "epoch nanoseconds"
)

// Magnitude thresholds separating one epoch unit from the next.
//
// 1e11 seconds is the year 5138 and 1e11 milliseconds is 1973, so every
// timestamp anybody is holding today lands on the right side of each line.
const (
	secondsCeil = uint64(1e11)
	millisCeil  = uint64(1e14)
	microsCeil  = uint64(1e17)
)

// maxSeconds bounds a NumericDate before it reaches time.Unix. 1e12 seconds is
// the year 33658 — far past anything meaningful and far short of what would
// overflow an int64 — so the bound refuses nonsense without refusing any real
// token.
const maxSeconds = 1e12

// ParseInstant parses raw as an exact instant. Layouts that carry no zone are
// read in loc; RFC3339's own offset wins over it, which is the point of
// writing one.
func ParseInstant(raw string, loc *time.Location) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, raw, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// Examples renders t once per accepted layout, for an error message that has
// to show what it would have taken.
//
// Examples rather than the layouts themselves: Go spells a layout as a date in
// 2006, which reads as an example right up to `Z07:00` — and a hint that hands
// somebody `2006-01-02T15:04:05Z07:00` has told them to type a string that is
// not a time. Formatting a real instant produces five spellings that all parse
// back, which is the property the hint is claiming.
func Examples(t time.Time) []string {
	out := make([]string, 0, len(layouts))
	for _, l := range layouts {
		out = append(out, t.Format(l))
	}
	return out
}

// ParseEpoch reads a bare integer as an instant, choosing the unit by
// magnitude, and says which unit it chose.
//
// The unit has to be guessed because nothing in the number carries it, and it
// has to be *reported* because the guess can be wrong. Between 1970 and 1973
// the ranges genuinely overlap — 86400000 is both the second day of 1970 in
// milliseconds and a day in 1972 in seconds — and no rule resolves that,
// because the ambiguity is in the input. So the honest contract is not "we get
// it right", it is "we say what we assumed", and every caller is expected to
// show the Unit beside the answer.
//
// Anything that is not a base-10 integer is not an epoch: a float, a hex
// string and a phone number all come back false rather than being coerced into
// a date somebody would then believe.
func ParseEpoch(raw string) (time.Time, Unit, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, "", false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, "", false
	}
	switch mag := magnitude(n); {
	case mag < secondsCeil:
		return time.Unix(n, 0).UTC(), Seconds, true
	case mag < millisCeil:
		return time.UnixMilli(n).UTC(), Millis, true
	case mag < microsCeil:
		return time.UnixMicro(n).UTC(), Micros, true
	default:
		return time.Unix(0, n).UTC(), Nanos, true
	}
}

// magnitude is |n| as a uint64, which negating an int64 cannot give you:
// -math.MinInt64 is still math.MinInt64, so a plain negation would hand the
// switch above a negative "magnitude", match the first case, and read the
// largest nanosecond timestamp there is as a second count.
func magnitude(n int64) uint64 {
	if n < 0 {
		return uint64(-(n + 1)) + 1
	}
	return uint64(n)
}

// FromSeconds converts a JSON number of seconds since the epoch — RFC 7519's
// NumericDate, which permits a fraction — to an instant.
//
// NaN and the infinities are tested for by name rather than left to the
// range check below, because every comparison against NaN is false: a bare
// `math.Abs(v) > maxSeconds` would let NaN through as if it were in range,
// and the caller would print the epoch it turned into.
func FromSeconds(v float64) (time.Time, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > maxSeconds {
		return time.Time{}, false
	}
	sec, frac := math.Modf(v)
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC(), true
}

// Stamp renders an instant the way it has to read beside the raw number it was
// decoded from: the exact UTC spelling, and how far away it is.
//
// Both halves, always. The exact spelling alone is what the token said and
// leaves the reader subtracting years in their head; "8 years ago" alone is
// not something you can paste into a query.
func Stamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339) + " (" + format.Ago(t) + ")"
}
