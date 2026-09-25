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
	"errors"
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
			if l == time.RFC3339 && offsetOutOfRange(raw) != "" {
				return time.Time{}, false
			}
			return t, true
		}
	}
	return time.Time{}, false
}

// offsetOutOfRange names the part of an RFC3339 offset no zone has: an hour
// of 24 or more, or a minute of 60 or more. raw must already have parsed as
// RFC3339, which leaves it ending in `Z` or in `±hh:mm`.
//
// Stricter than Go, deliberately. Its parser refuses only an hour past 24 or
// a minute past 60, "as some people do write offsets of 24 hours or 60
// minutes", so +24:59 was converted and +05:60 was quietly read as +06:00 —
// while RangeHint told the person refused for +25:00 that an offset is under
// 24 hours, a rule the accepted one had just broken. No zone in use is
// further from UTC than 14 hours, so the hint's rule is the one applied.
func offsetOutOfRange(raw string) string {
	if len(raw) < len("+00:00") {
		return ""
	}
	tail := raw[len(raw)-len("+00:00"):]
	if (tail[0] != '+' && tail[0] != '-') || tail[3] != ':' {
		return ""
	}
	hour, herr := strconv.Atoi(tail[1:3])
	minute, merr := strconv.Atoi(tail[4:6])
	switch {
	case herr != nil || merr != nil:
		return ""
	case hour >= 24:
		return "time zone offset hour"
	case minute >= 60:
		return "time zone offset minute"
	}
	return ""
}

// OutOfRange explains why raw, written in one of the accepted layouts, is
// still not an instant: the field that does not exist — "day", "month",
// "hour", "time zone offset hour" — or "" when raw is not written that way at
// all.
//
// "Not an instant this understands" said of 2026-02-30 is wrong twice: the
// shape is understood perfectly, and the list of accepted shapes that follows
// the refusal includes the one that was typed. What is wrong is that February
// has no 30th, and time.Parse already says so; the refusal just dropped it.
//
// **A range error alone does not mean raw is written in that layout.** Go's
// parser checks each field's range the moment it reads the field and returns
// there, before it has looked at the rest of the value — only the day is
// checked after the whole string has matched. So "2026-13-01 garbage" came
// back as a month out of range, the caller said it "is written as a date",
// and correcting the month earned "not an instant this understands": the
// first answer had sent the person to fix the wrong thing. The error counts
// only when raw also has the layout's shape.
func OutOfRange(raw string, loc *time.Location) string {
	raw = strings.TrimSpace(raw)
	for _, l := range layouts {
		_, err := time.ParseInLocation(l, raw, loc)
		if err == nil && l == time.RFC3339 {
			return offsetOutOfRange(raw)
		}
		var pe *time.ParseError
		if errors.As(err, &pe) && strings.HasSuffix(pe.Message, " out of range") && hasShape(raw, l) {
			return strings.TrimSuffix(strings.TrimPrefix(pe.Message, ": "), " out of range")
		}
	}
	return ""
}

// hasShape reports whether raw is laid out as layout is, every digit taken as
// any digit: "2026-13-01" has the shape of 2006-01-02, and "2026-13-01 x" and
// "2026-13" do not.
//
// The shape is read off a rendering of the layout rather than the layout
// string, for the reason Examples gives, and in three zones, because RFC3339
// spells its offset `Z`, `+hh:mm` or `-hh:mm`.
//
// Each rendering is also compared with its hour cut to one digit. Go reads the
// hour element in one digit or two, so "2026-09-24 9:30" parses, but Format
// always writes two, and a shape taken from the rendering alone refused every
// hand-typed morning hour: "2026-09-24 9:61" was "not an instant this
// understands" instead of a minute out of range, the wrong-twice answer
// OutOfRange exists to prevent. Rendering an instant at five o'clock would
// not help: Format writes that hour as "05". Go reads every other element of
// these layouts at the one width Format writes.
//
// A fraction after the seconds is set aside first: Go reads one after any
// seconds field, layout or no, so 10:00:00.5 is written in a layout that has
// seconds. It is found by its place in raw, straight after a time's seconds,
// and not at the layout's offset for the seconds, which a one-digit hour moves
// one byte to the left.
func hasShape(raw, layout string) bool {
	if strings.Contains(layout, ":05") {
		raw = withoutFraction(raw)
	}
	shape := digitMask(raw)
	east, west := time.FixedZone("", 5*3600+30*60), time.FixedZone("", -5*3600-30*60)
	for _, zone := range []*time.Location{time.UTC, east, west} {
		full := time.Date(2006, 1, 2, 15, 4, 5, 0, zone).Format(layout)
		if shape == digitMask(full) || shape == digitMask(strings.Replace(full, "15:", "5:", 1)) {
			return true
		}
	}
	return false
}

// withoutFraction drops the `.digits` or `,digits` run that follows the two
// digits of a time's seconds (`:mm:ss`), and returns raw unchanged when there
// is none. A fraction after the minutes is left in place: Go does not read one
// there, so a value carrying it has no layout's shape.
func withoutFraction(raw string) string {
	const minutesSeconds = ":00:00"
	for at := len(minutesSeconds); at+1 < len(raw); at++ {
		fraction := (raw[at] == '.' || raw[at] == ',') && isDigit(raw[at+1])
		if !fraction || digitMask(raw[at-len(minutesSeconds):at]) != minutesSeconds {
			continue
		}
		end := at + 1
		for end < len(raw) && isDigit(raw[end]) {
			end++
		}
		return raw[:at] + raw[end:]
	}
	return raw
}

func digitMask(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return '0'
		}
		return r
	}, s)
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// RangeHint is what to say beside OutOfRange's answer.
const RangeHint = "every part has to exist: months 01 to 12, the days that month has, hours 00 to 23, " +
	"minutes and seconds 00 to 59, and an offset under 24 hours whose minutes run 00 to 59 too"

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
		return uint64(-(n + 1)) + 1 //nolint:gosec // n < 0 here, so -(n + 1) is 0 or more
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
	return t.UTC().Format(time.RFC3339) + " (" + format.Relative(t) + ")"
}
