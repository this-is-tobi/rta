package plugin

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A Duration input is a length of time written with its unit: "30s", "5m",
// "2h", "1d".
//
// **Carried as text everywhere, and that is the design.** An Int named
// `timeout` says nothing about whether it counts seconds or milliseconds, and
// the answer lived in a Help string, so `--timeout 30` meant 30 seconds in one
// capability, 30 milliseconds in another and 30 minutes in a third. A number
// with a unit attached cannot be misread, and text is what the CLI types, an
// agent sends, a form edits and a config file holds, with no conversion on
// the way for one of those to get wrong. The wire carries a string; the
// handler reads a time.Duration (Request.Duration).
//
// A bare number is refused rather than read as seconds: the host would be
// guessing for the plugin that meant milliseconds, and "30" is the one
// spelling every reader has a different default for.

// durationUnits are the suffixes a Duration is written with: Go's, and the two
// a person reaches for when a time spans days. A day is twenty-four hours and a
// week seven of them, as a TTL means it; neither is a calendar unit.
var durationUnits = map[string]uint64{
	"ns": uint64(time.Nanosecond),
	"us": uint64(time.Microsecond),
	"µs": uint64(time.Microsecond), // U+00B5 MICRO SIGN
	"μs": uint64(time.Microsecond), // U+03BC GREEK SMALL LETTER MU
	"ms": uint64(time.Millisecond),
	"s":  uint64(time.Second),
	"m":  uint64(time.Minute),
	"h":  uint64(time.Hour),
	"d":  uint64(24 * time.Hour),
	"w":  uint64(7 * 24 * time.Hour),
}

// ParseDuration reads a length of time as a Duration input is written: a
// sequence of numbers each followed by its unit, "1h30m" or "2d" or "1.5h".
// The units are Go's (ns, us, ms, s, m, h) and d and w for days and weeks. It
// is time.ParseDuration with those two added, and the same grammar otherwise:
// no sign, no spaces, and a bare "0" as the one number allowed without a unit.
//
// Exported because the host and the handler have to agree on what a value
// means, and two parsers are how they come not to.
func ParseDuration(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if s == "" {
		return 0, errors.New("empty")
	}
	var total uint64
	rest := s
	for rest != "" {
		i := 0
		for i < len(rest) && isNumberByte(rest[i]) {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("expected a number at %q", rest)
		}
		number := rest[:i]
		rest = rest[i:]
		j := 0
		for j < len(rest) && !isNumberByte(rest[j]) {
			j++
		}
		unit := rest[:j]
		rest = rest[j:]
		if unit == "" {
			return 0, errors.New("missing a unit")
		}
		scale, ok := durationUnits[unit]
		if !ok {
			return 0, fmt.Errorf("unknown unit %q", unit)
		}
		whole, frac, _ := strings.Cut(number, ".")
		var n uint64
		if whole != "" {
			var err error
			if n, err = strconv.ParseUint(whole, 10, 63); err != nil {
				return 0, errors.New("too large")
			}
		} else if frac == "" {
			return 0, fmt.Errorf("expected a number at %q", number)
		}
		if n > math.MaxInt64/scale {
			return 0, errors.New("too large")
		}
		total += n * scale
		if frac != "" {
			f, err := strconv.ParseFloat("0."+frac, 64)
			if err != nil {
				return 0, fmt.Errorf("expected a number at %q", number)
			}
			total += uint64(f * float64(scale))
		}
		if total > math.MaxInt64 {
			return 0, errors.New("too large")
		}
	}
	return time.Duration(total), nil
}

func isNumberByte(b byte) bool { return b >= '0' && b <= '9' || b == '.' }

// FormatDuration is d written the way a person writes one: whole days as
// "2d", otherwise Go's spelling without the zero units it pads with ("1h", not
// "1h0m0s"). What ParseDuration reads back as d.
func FormatDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0s"
	case d < 0:
		return d.String()
	case d%(24*time.Hour) == 0:
		return strconv.FormatInt(int64(d/(24*time.Hour)), 10) + "d"
	}
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}

// toDuration reads v as a Duration input's value: the text it is declared and
// sent as, or a time.Duration an in-process caller built. Nothing else — a
// number is refused, so a value whose unit nobody wrote is never guessed at.
func toDuration(v any) (time.Duration, bool) {
	switch d := v.(type) {
	case string:
		parsed, err := ParseDuration(d)
		return parsed, err == nil
	case time.Duration:
		return d, true
	}
	return 0, false
}

// bareNumber reports whether v is a number nobody gave a unit: a numeric value
// as a config file or an agent's JSON carries one, or the digits a command-line
// flag hands over for it, since every flag arrives as text. It is the one
// mistake in writing a duration common enough to be named as what it is.
func bareNumber(v any) bool {
	if text, isText := v.(string); isText {
		return bareNumberText.MatchString(text)
	}
	return statedShape(v) == "a number"
}

var bareNumberText = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

// Duration reads a Duration input. An input nobody gave, and one that is not a
// duration, read as zero, as Int does: the host has already refused the second
// (CheckInputs) before a handler runs.
func (r Request) Duration(name string) time.Duration {
	d, _ := toDuration(r.values[name])
	return d
}

// DurationPattern is the grammar ParseDuration reads, as the regular expression
// a JSON Schema carries for a string: the one place an agent is told a Duration
// is more than text, since its schema type is "string" and its help says what
// the number is for, not how to write it. Anchored, and the same in RE2 and
// ECMAScript, which is what a schema-enforcing client runs.
//
// What it matches ParseDuration accepts, bar the two spellings no one types
// (".5s" and "1.s"), which the host reads and a schema need not advertise.
const DurationPattern = `^(?:0|(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|μs|ms|s|m|h|d|w))+)$`

// durationHint is how a refusal says to write one.
const durationHint = "write it with a unit: 30s, 5m, 2h or 1d"
