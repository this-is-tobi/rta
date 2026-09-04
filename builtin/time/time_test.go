package time

import (
	"context"
	"strings"
	"testing"
	stdtime "time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, false)
}

// reference is a fixed instant every relative assertion below hangs off, so
// none of them depend on when the suite runs.
var reference = stdtime.Date(2026, 9, 4, 12, 0, 0, 0, stdtime.UTC)

func TestPluginIsValid(t *testing.T) {
	if err := Plugin().Validate(); err != nil {
		t.Fatal(err)
	}
}

func at(t *testing.T, values map[string]any) view.KeyValue {
	t.Helper()
	v, err := runAt(context.Background(), req(values))
	if err != nil {
		t.Fatalf("time.at %v: %v", values, err)
	}
	return v.(view.KeyValue)
}

func value(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// Every form somebody might have the instant in has to land on the same
// instant. This is the capability's whole contract in one test: the spellings
// differ, the answer does not.
func TestEveryAcceptedSpellingOfOneInstantAgrees(t *testing.T) {
	const want = "2018-01-18T02:30:22Z"
	for _, when := range []string{
		"1516242622",
		"1516242622000",
		"2018-01-18T02:30:22Z",
		"2018-01-18T03:30:22+01:00",
	} {
		t.Run(when, func(t *testing.T) {
			if got := value(at(t, map[string]any{"when": when}), "utc"); got != want {
				t.Errorf("utc = %q, want %q", got, want)
			}
		})
	}
}

// The unit of a bare number is guessed, and between 1970 and 1973 the ranges
// genuinely overlap — so the guess is only honest if it is shown. A reader who
// cannot see which unit was assumed cannot tell a right answer from a wrong
// one.
func TestABareNumberSaysWhichUnitItWasReadAs(t *testing.T) {
	if got := value(at(t, map[string]any{"when": "1516242622"}), "read-as"); got != "epoch seconds" {
		t.Errorf("read-as = %q, want it to name the unit", got)
	}
	if got := value(at(t, map[string]any{"when": "1516242622000"}), "read-as"); got != "epoch milliseconds" {
		t.Errorf("read-as = %q, want it to name the unit", got)
	}
	// Nothing was assumed about a spelling that carried its own unit, and a
	// row saying so would be noise on the common path.
	if got := value(at(t, map[string]any{"when": "2018-01-18T02:30:22Z"}), "read-as"); got != "" {
		t.Errorf("read-as = %q on an exact time, want no such row", got)
	}
}

// `-90m` cannot be typed as a CLI argument without `--` in front of it, so the
// worded forms are not sugar — on the surface this capability is most used
// from, they are the ones that work.
func TestADurationCarriesItsOwnDirection(t *testing.T) {
	for _, tc := range []struct {
		when string
		want stdtime.Duration
	}{
		{"-90m", -90 * stdtime.Minute},
		{"+90m", 90 * stdtime.Minute},
		{"90m ago", -90 * stdtime.Minute},
		{"in 90m", 90 * stdtime.Minute},
		{"IN 90m", 90 * stdtime.Minute},
		{"90m AGO", -90 * stdtime.Minute},
	} {
		t.Run(tc.when, func(t *testing.T) {
			got, unit, err := resolve(tc.when, reference)
			if err != nil {
				t.Fatalf("resolve(%q): %v", tc.when, err)
			}
			if unit != "" {
				t.Errorf("resolve(%q) claimed to guess a unit (%s)", tc.when, unit)
			}
			if want := reference.Add(tc.want); !got.Equal(want) {
				t.Errorf("resolve(%q) = %s, want %s", tc.when, got, want)
			}
		})
	}
}

// A bare `2h` is understood perfectly and is still not an instant. Picking a
// direction would be wrong half the time, and "I do not understand that" would
// be a lie — so it gets its own refusal, naming both spellings that would have
// worked.
func TestAnUnsignedDurationIsRefusedByNamingTheTwoThatWork(t *testing.T) {
	_, _, err := resolve("2h", reference)
	if err == nil {
		t.Fatal("a bare duration was accepted, direction and all")
	}
	if err.Code != "time.at.unsigned" {
		t.Errorf("code = %q, want time.at.unsigned", err.Code)
	}
	if !strings.Contains(err.Hint, "2h ago") || !strings.Contains(err.Hint, "in 2h") {
		t.Errorf("hint = %q, want both directions spelled out with the value typed", err.Hint)
	}
}

// The hint on an unreadable input has to list spellings that actually parse.
// It once listed Go's own layout strings, which put `2006-01-02T15:04:05Z07:00`
// in front of somebody as though it were a time they could type.
func TestTheUnreadableHintOffersOnlySpellingsThatParse(t *testing.T) {
	_, _, err := resolve("yesterday", reference)
	if err == nil {
		t.Fatal("`yesterday` was accepted")
	}
	if err.Code != "time.at.unreadable" {
		t.Errorf("code = %q, want time.at.unreadable", err.Code)
	}
	if strings.Contains(err.Hint, "Z07:00") || strings.Contains(err.Hint, "2006-01-02") {
		t.Errorf("hint hands over a Go layout instead of an example: %q", err.Hint)
	}
	if !strings.Contains(err.Hint, "2026-09-04") {
		t.Errorf("hint = %q, want examples built from the instant it was asked about", err.Hint)
	}
}

// Empty and absent both mean now, because `when` has a default and a form can
// still submit a blank one.
func TestNothingAtAllMeansNow(t *testing.T) {
	for _, when := range []string{"", "now", "NOW"} {
		got, _, err := resolve(when, reference)
		if err != nil {
			t.Fatalf("resolve(%q): %v", when, err)
		}
		if !got.Equal(reference) {
			t.Errorf("resolve(%q) = %s, want now", when, got)
		}
	}
}

// The zone row is keyed by the zone it is showing, so a reader who asked for
// two of them can tell which is which — and the offset actually changes.
func TestANamedZoneIsShownUnderItsOwnName(t *testing.T) {
	// The zone database is the host's, not rta's — a stripped container has
	// none. Skipping says that plainly rather than failing as though the
	// rendering were wrong.
	if _, err := stdtime.LoadLocation("Asia/Tokyo"); err != nil {
		t.Skipf("no IANA zone database on this host: %v", err)
	}
	kv := at(t, map[string]any{"when": "1516242622", "zone": "Asia/Tokyo"})
	got := value(kv, "Asia/Tokyo")
	if got == "" {
		t.Fatalf("no Asia/Tokyo row in %+v", kv.Pairs)
	}
	if !strings.HasPrefix(got, "2018-01-18T11:30:22+09:00") {
		t.Errorf("Asia/Tokyo = %q, want the instant at +09:00", got)
	}
	// The abbreviation is what distinguishes one offset of a zone from the
	// other; RFC3339 alone cannot say whether +02:00 is summer or a different
	// country.
	if !strings.HasSuffix(got, "JST") {
		t.Errorf("Asia/Tokyo = %q, want the zone abbreviation", got)
	}
}

func TestAZoneThisMachineCannotLoadIsRefused(t *testing.T) {
	_, err := runAt(context.Background(), req(map[string]any{"when": "now", "zone": "Mars/Olympus"}))
	if err == nil {
		t.Fatal("an unknown zone was accepted")
	}
	verr, ok := err.(*view.Error)
	if !ok {
		t.Fatalf("error is %T, want *view.Error", err)
	}
	if verr.Code != "time.at.zone" {
		t.Errorf("code = %q, want time.at.zone", verr.Code)
	}
}

// The rows a caller scripts against: UTC for correlation, epoch for querying,
// and the two of them describing the same instant.
func TestTheEpochRowsAndTheUTCRowDescribeOneInstant(t *testing.T) {
	kv := at(t, map[string]any{"when": "2018-01-18T02:30:22Z"})
	if got := value(kv, "epoch"); got != "1516242622" {
		t.Errorf("epoch = %q, want 1516242622", got)
	}
	if got := value(kv, "epoch-ms"); got != "1516242622000" {
		t.Errorf("epoch-ms = %q, want 1516242622000", got)
	}
	if got := value(kv, "relative"); !strings.Contains(got, "ago") {
		t.Errorf("relative = %q, want a past instant to read as past", got)
	}
}
