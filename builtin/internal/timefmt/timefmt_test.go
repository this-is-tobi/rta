package timefmt

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The unit is chosen by magnitude, and each of the four has to be recognised
// from a real timestamp of that precision rather than from a digit count that
// happens to work for one value.
func TestEachEpochPrecisionIsRecognisedFromItsMagnitude(t *testing.T) {
	// One instant, spelled four ways.
	const want = "2018-01-18T02:30:22Z"
	for _, tc := range []struct {
		raw  string
		unit Unit
	}{
		{"1516242622", Seconds},
		{"1516242622000", Millis},
		{"1516242622000000", Micros},
		{"1516242622000000000", Nanos},
	} {
		t.Run(string(tc.unit), func(t *testing.T) {
			got, unit, ok := ParseEpoch(tc.raw)
			if !ok {
				t.Fatalf("ParseEpoch(%q) refused a valid epoch", tc.raw)
			}
			if unit != tc.unit {
				t.Errorf("ParseEpoch(%q) read it as %s, want %s", tc.raw, unit, tc.unit)
			}
			if stamp := got.Format(time.RFC3339); stamp != want {
				t.Errorf("ParseEpoch(%q) = %s, want %s", tc.raw, stamp, want)
			}
		})
	}
}

// math.MinInt64 is the one value where negating an int64 does not change its
// sign, so a magnitude computed by negation stays negative — which compares
// below every ceiling and reads the largest nanosecond timestamp there is as a
// second count, placing it hundreds of millions of years off.
func TestTheLargestNegativeTimestampIsNotReadAsSeconds(t *testing.T) {
	got, unit, ok := ParseEpoch(strconv.FormatInt(math.MinInt64, 10))
	if !ok {
		t.Fatal("ParseEpoch refused math.MinInt64")
	}
	if unit != Nanos {
		t.Errorf("unit = %s, want %s — a value this large is nanoseconds or nothing", unit, Nanos)
	}
	if want := time.Unix(0, math.MinInt64).UTC(); !got.Equal(want) {
		t.Errorf("ParseEpoch(MinInt64) = %s, want %s", got, want)
	}
}

// An epoch is a base-10 integer. Anything else is somebody's version string,
// hash prefix or phone number, and coercing one into a date produces an answer
// that looks exactly like a real one.
func TestOnlyAnIntegerIsAnEpoch(t *testing.T) {
	for _, raw := range []string{"", "  ", "1.5", "0x5A", "1e9", "1516242622abc", "yesterday", "--1"} {
		if _, _, ok := ParseEpoch(raw); ok {
			t.Errorf("ParseEpoch(%q) accepted something that is not an epoch", raw)
		}
	}
}

// FromSeconds guards the range before time.Unix sees it. NaN is listed by name
// because every comparison against it is false: a lone `math.Abs(v) > max`
// would answer "in range" and hand time.Unix a number that is not one.
func TestANumericDateOutsideTheRangeOfATimeIsRefused(t *testing.T) {
	for _, v := range []float64{
		math.NaN(),
		math.Inf(1),
		math.Inf(-1),
		1e300,
		-1e300,
	} {
		if got, ok := FromSeconds(v); ok {
			t.Errorf("FromSeconds(%v) = %s, want a refusal", v, got)
		}
	}
}

// RFC 7519 permits a NumericDate to carry a fraction, so a token that spells
// its expiry to the millisecond must not lose it.
func TestANumericDateKeepsItsFraction(t *testing.T) {
	got, ok := FromSeconds(1516242622.5)
	if !ok {
		t.Fatal("FromSeconds refused a fractional NumericDate")
	}
	if want := time.Unix(1516242622, 500*int64(time.Millisecond)).UTC(); !got.Equal(want) {
		t.Errorf("FromSeconds = %s, want %s", got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
	}
}

// Examples exists to be pasted back in. Every spelling it prints has to be one
// ParseInstant accepts, or the hint is telling somebody to type a string that
// does not work — which is the exact failure it was written to fix, Go's
// `Z07:00` having been rendered as though it were an example.
func TestEveryExampleParsesBackAsAnInstant(t *testing.T) {
	at := time.Date(2026, 9, 4, 15, 4, 5, 0, time.UTC)
	examples := Examples(at)
	if len(examples) == 0 {
		t.Fatal("Examples returned nothing")
	}
	for _, e := range examples {
		if strings.Contains(e, "Z07:00") || strings.Contains(e, "2006") {
			t.Errorf("example %q is a Go layout, not a time somebody can type", e)
		}
		if _, ok := ParseInstant(e, time.UTC); !ok {
			t.Errorf("Examples printed %q, which ParseInstant refuses", e)
		}
	}
	// The full-precision spelling has to survive the round trip exactly; the
	// shorter ones legitimately drop what they do not carry.
	if back, ok := ParseInstant(examples[0], time.UTC); !ok || !back.Equal(at) {
		t.Errorf("round trip of %q = %s, want %s", examples[0], back, at)
	}
}

// A spelling with no zone in it means the caller's zone, not UTC. Getting this
// backwards silently shifts every hand-typed time by the offset.
func TestAZonelessSpellingIsReadInTheGivenLocation(t *testing.T) {
	loc := time.FixedZone("test", 5*3600)
	got, ok := ParseInstant("2026-09-04 12:00:00", loc)
	if !ok {
		t.Fatal("ParseInstant refused a zoneless spelling")
	}
	if want := time.Date(2026, 9, 4, 12, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("ParseInstant = %s, want %s", got, want)
	}
	// An RFC3339 offset is the whole reason to write one, so it wins over loc.
	got, ok = ParseInstant("2026-09-04T12:00:00Z", loc)
	if !ok {
		t.Fatal("ParseInstant refused RFC3339")
	}
	if want := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Errorf("ParseInstant ignored the offset it was given: %s, want %s", got, want)
	}
}

// Stamp carries both halves because each is useless for the other's job: the
// exact spelling cannot be read at a glance, and the relative phrase cannot be
// pasted anywhere.
func TestStampCarriesBothTheExactTimeAndTheDistance(t *testing.T) {
	got := Stamp(time.Now().Add(-2 * time.Hour))
	if !strings.Contains(got, "T") || !strings.HasSuffix(got, ")") {
		t.Errorf("Stamp = %q, want an RFC3339 stamp and a parenthesised distance", got)
	}
	if !strings.Contains(got, "2 hours ago") {
		t.Errorf("Stamp = %q, want it to say how long ago", got)
	}
}
