package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A time of day the local clock showed twice or never was read as one instant
// without a word, and a filter on the record is the place that costs: a
// skipped reading moved an hour on drops the first half hour of calls from
// the answer. One the clock never showed is refused, as `time at` refuses it;
// one it showed twice lists from the earlier and says the other exists.
func TestSinceNamesAClockReadingTheZoneShowedTwiceOrNever(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tz database to read Europe/Paris from")
	}
	t.Cleanup(func(prev *time.Location) func() { return func() { time.Local = prev } }(time.Local))
	time.Local = paris

	_, _, verr := parseSince("2026-03-29 02:30")
	if verr == nil || verr.Code != "agent.log.since" || !strings.Contains(verr.Hint, "2026-03-29T02:30:00+01:00") {
		t.Errorf("a time the clock skipped: %v, want it refused with the offset that names it", verr)
	}

	since, note, verr := parseSince("2026-10-25 02:30")
	if verr != nil {
		t.Fatal(verr)
	}
	if want := time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC); !since.Equal(want) {
		t.Errorf("a time shown twice read as %s, want the earlier, %s", since.UTC(), want)
	}
	if !strings.Contains(note, "2026-10-25T02:30:00+01:00") || !strings.Contains(note, "offset") {
		t.Errorf("note = %q, want the later instant named and the way out said", note)
	}

	for _, fine := range []string{"2026-10-25 01:30", "2026-10-25 03:30", "2026-10-25T02:30:00+02:00", "2h", "2026-10-25"} {
		if _, note, verr := parseSince(fine); verr != nil || note != "" {
			t.Errorf("%q: note %q, error %v, want a plain reading", fine, note, verr)
		}
	}

	t.Setenv("RTA_DATA_DIR", t.TempDir())
	v, err := run(t, "agent.log", map[string]any{"since": "2026-10-25 02:30"})
	if err != nil {
		t.Fatal(err)
	}
	if tbl, ok := v.(view.Table); !ok || len(tbl.Warnings) != 1 || tbl.Warnings[0].Code != "agent.log.since" {
		t.Errorf("the listing carries no warning for the reading it made: %+v", v)
	}
}
