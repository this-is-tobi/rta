package format

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// One grammar for every length of time a person writes, wherever it is written:
// a flag, a form box, a config file, a role, a grant, a lock, a profile's deadline,
// a tile's pace, an agent's JSON. "30s", "5m", "2h", "1d", "1w", "1h30m".
//
// **One, because the same sentence had to be learned again in every place.** A
// profile's `ttl:`, a role's, a grant's `--ttl` and a policy's `maxTTL` were read
// by Go's time.ParseDuration, which has no day, so `ttl: 1d` was refused in a
// config file where `rta lock add --ttl 1d` and a Duration input took it, and a
// grant told the person who typed `1d` that "there is no day unit". A rule that
// holds in one place and not in the next is how a person comes to read the
// documentation for every key, and the answer to "can I write a day here" is now
// always yes. What a place may then *hold* (a grant lasts at most a day) is that
// place's bound and is said in its own words, not by a parser that cannot read.

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

// DurationPattern is the grammar ParseDuration reads, as the regular expression
// a JSON Schema carries for a string: the one place an agent is told a Duration
// is more than text, since its schema type is "string" and its help says what
// the number is for, not how to write it. Anchored, and the same in RE2 and
// ECMAScript, which is what a schema-enforcing client runs.
//
// What it matches ParseDuration accepts, bar the two spellings no one types
// (".5s" and "1.s"), which the host reads and a schema need not advertise.
const DurationPattern = `^(?:0|(?:[0-9]+(?:\.[0-9]+)?(?:ns|us|µs|μs|ms|s|m|h|d|w))+)$`
