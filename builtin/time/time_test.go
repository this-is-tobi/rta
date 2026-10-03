package time

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	stdtime "time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// February 30th is understood perfectly and does not exist. Answering "not an
// instant this understands" and then listing the very shape that was typed
// sends somebody hunting for a formatting mistake they did not make.
func TestADateThatDoesNotExistSaysWhichPartDoesNot(t *testing.T) {
	_, _, err := resolve("2026-02-30", reference)
	if err == nil {
		t.Fatal("February 30th was accepted")
	}
	if err.Code != "time.at.unreadable" || !strings.Contains(err.Message, "its day is out of range") {
		t.Errorf("got %s: %s", err.Code, err.Message)
	}
	if strings.Contains(err.Message, "understands") {
		t.Errorf("message = %q, still says the shape was not understood", err.Message)
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

// ISO 8601's short spelling, with the T and without seconds, is the same minute
// as the space form that was already read.
func TestTheShortISOSpellingIsTheSameMinuteAsTheSpaceForm(t *testing.T) {
	loc := stdtime.FixedZone("test", 2*3600)
	t.Cleanup(func(prev *stdtime.Location) func() { return func() { stdtime.Local = prev } }(stdtime.Local))
	stdtime.Local = loc
	short, _, err := resolve("2026-09-04T12:30", reference)
	if err != nil {
		t.Fatalf("the T form without seconds was refused: %v", err)
	}
	spaced, _, err := resolve("2026-09-04 12:30", reference)
	if err != nil || !short.Equal(spaced) {
		t.Errorf("T form = %s, space form = %s (%v)", short, spaced, err)
	}
}

// A reading of the clock that the zone skipped — 02:30 on the night the clocks
// went from 02:00 to 03:00 — was moved to 03:30 and answered as though it were
// the instant typed. It is refused, with the offset that names the one meant.
func TestAWallClockTheZoneSkippedIsRefusedNotMoved(t *testing.T) {
	paris, err := stdtime.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database to read Europe/Paris from")
	}
	t.Cleanup(func(prev *stdtime.Location) func() { return func() { stdtime.Local = prev } }(stdtime.Local))
	stdtime.Local = paris

	_, _, verr := resolve("2026-03-29 02:30", reference)
	if verr == nil || verr.Code != "time.at.skipped" {
		t.Fatalf("a time the clock skipped = %+v, want time.at.skipped", verr)
	}
	if !strings.Contains(verr.Hint, "2026-03-29T02:30:00+01:00") {
		t.Errorf("hint %q does not name the instant with the offset in force before the change", verr.Hint)
	}
	for _, fine := range []string{"2026-03-29 01:30", "2026-03-29 03:30", "2026-03-29T02:30:00+01:00", "2026-10-25 02:30"} {
		if _, _, verr := resolve(fine, reference); verr != nil {
			t.Errorf("%q was refused: %v", fine, verr)
		}
	}
}

// The other half of the clock changing: the night it goes back, 02:30 shows
// twice. Go reads it as one of the two and said nothing of the other, so the
// answer was right for an instant the person may not have meant and gave no
// hint that another existed an hour away. It is answered, as the one it read,
// with the other named beside it; a reading that happened once, and one that
// carries its own offset, say nothing.
func TestAWallClockTheZoneShowedTwiceNamesTheOtherInstant(t *testing.T) {
	paris, err := stdtime.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database to read Europe/Paris from")
	}
	t.Cleanup(func(prev *stdtime.Location) func() { return func() { stdtime.Local = prev } }(stdtime.Local))
	stdtime.Local = paris

	kv := at(t, map[string]any{"when": "2026-10-25 02:30"})
	note := value(kv, "ambiguous")
	if note == "" {
		t.Fatalf("a reading the clock showed twice has no ambiguous row: %+v", kv.Pairs)
	}
	earlier := stdtime.Date(2026, 10, 25, 0, 30, 0, 0, stdtime.UTC).Unix()
	later := earlier + 3600
	utc := value(kv, "epoch")
	var other int64
	switch utc {
	case strconv.FormatInt(earlier, 10):
		other = later
	case strconv.FormatInt(later, 10):
		other = earlier
	default:
		t.Fatalf("epoch = %s, want one of the two instants %d and %d", utc, earlier, later)
	}
	if !strings.Contains(note, "(epoch "+strconv.FormatInt(other, 10)+")") || !strings.Contains(note, "offset") {
		t.Errorf("ambiguous = %q, want the other instant (epoch %d) named and the way out said", note, other)
	}

	for _, fine := range []string{"2026-10-25 01:30", "2026-10-25 03:30", "2026-10-25T02:30:00+02:00", "2026-03-29 03:30"} {
		if v := value(at(t, map[string]any{"when": fine}), "ambiguous"); v != "" {
			t.Errorf("%q said it was ambiguous: %q", fine, v)
		}
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
	var verr *view.Error
	ok := errors.As(err, &verr)
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
