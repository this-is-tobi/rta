package format

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	tests := []struct {
		in   uint64
		want string
	}{
		{512, "512 B"},
		{2048, "2.0 KiB"},
		{3 * 1024 * 1024 * 1024, "3.0 GiB"},
	}
	for _, tt := range tests {
		if got := Bytes(tt.in); got != tt.want {
			t.Errorf("Bytes(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Ago answers "is this recent?" in one unit, which is the only question a
// timestamp on a dashboard is read for.
func TestAgo(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{now.Add(-200 * time.Millisecond), "just now"},
		{now.Add(-12 * time.Second), "12 seconds ago"},
		{now.Add(-time.Minute), "1 minute ago"},
		{now.Add(-90 * time.Second), "2 minutes ago"}, // rounded within the unit
		{now.Add(-59 * time.Minute), "59 minutes ago"},
		{now.Add(-3 * time.Hour), "3 hours ago"},
		{now.Add(-50 * time.Hour), "2 days ago"},
		{now.Add(-13 * 24 * time.Hour), "2 weeks ago"},
		{now.Add(-800 * 24 * time.Hour), "2 years ago"},
		{now.Add(3 * time.Minute), "in 3 minutes"},
		{time.Time{}, "never"},
	} {
		got := Ago(c.at)
		if got != c.want {
			t.Errorf("Ago(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}

// An instant centuries away is counted in calendar years. time.Since saturates
// at about 292 years, so a token's exp of 9999-12-31, the usual way to write
// "does not expire", read "in -9223372036 seconds" once negated, and 1700 was
// "292 years ago". The zero time is an instant too — 1 January of year 1, a
// date a token can carry — and only Ago, for a caller whose zero means that
// nothing happened yet, reads it as "never".
func TestRelativeCountsAFarInstantInYears(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		at   time.Time
		want string
	}{
		{time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), "in 7973 years"},
		{time.Date(2400, 1, 1, 0, 0, 0, 0, time.UTC), "in 373 years"},
		{time.Date(1700, 1, 1, 0, 0, 0, 0, time.UTC), "327 years ago"},
		{time.Time{}, "2026 years ago"},
		{now.AddDate(-291, 0, 0), "291 years ago"},
		{now.AddDate(289, 0, 0), "in 289 years"},
		{now.AddDate(-2, 0, 0), "2 years ago"},
		{now.Add(3 * time.Minute), "in 3 minutes"},
	} {
		if got := relativeTo(c.at, now); got != c.want {
			t.Errorf("relative %v from %v = %q, want %q", c.at, now, got, c.want)
		}
	}
	if got := Ago(time.Time{}); got != "never" {
		t.Errorf("Ago(zero) = %q, want never", got)
	}
	if got := Relative(time.Time{}); got == "never" || !strings.HasSuffix(got, "years ago") {
		t.Errorf("Relative(zero) = %q, want the years since year 1", got)
	}
}

// Bytes takes the type the caller is holding, which is the whole point of the
// signature: io.Copy hands over an int64, len an int, a client library whatever
// it declared. A caller that has to convert is a caller writing the conversion,
// and fifty-three of them wrote it.
func TestBytesTakesWhateverIntegerTypeTheCallerHolds(t *testing.T) {
	const want = "2.0 KiB"
	got := map[string]string{
		"int":    Bytes(int(2048)),
		"int32":  Bytes(int32(2048)),
		"int64":  Bytes(int64(2048)),
		"uint":   Bytes(uint(2048)),
		"uint32": Bytes(uint32(2048)),
		"uint64": Bytes(uint64(2048)),
		"len":    Bytes(len(make([]byte, 2048))),
	}
	for kind, s := range got {
		if s != want {
			t.Errorf("Bytes(%s) = %q, want %q", kind, s, want)
		}
	}
}

// A negative byte count is a bug in whoever computed it, and it now reads as
// one. Under the uint64 signature it wrapped instead: -1 rendered "16.0 EiB",
// which is a number a reader believes.
func TestBytesKeepsTheSignOfANegativeCount(t *testing.T) {
	for _, c := range []struct {
		in   int64
		want string
	}{
		{-1, "-1 B"},
		{-2048, "-2.0 KiB"},
		{math.MinInt64, "-8.0 EiB"}, // -n is still negative here; -(n+1)+1 is not
	} {
		if got := Bytes(c.in); got != c.want {
			t.Errorf("Bytes(%d) = %q, want %q", c.in, got, c.want)
		}
	}
	// The same minimum in the narrowest signed type, because the arithmetic that
	// survives it is per-type, not per-int64.
	if got := Bytes(int8(math.MinInt8)); got != "-128 B" {
		t.Errorf("Bytes(int8 minimum) = %q, want %q", got, "-128 B")
	}
}
