package format

import (
	"testing"
	"time"
)

func TestDurationReadsLikeSomethingAPersonWouldType(t *testing.T) {
	cases := map[time.Duration]string{
		24 * time.Hour:                                 "24h",
		time.Hour + 30*time.Minute:                     "1h30m",
		23*time.Hour + 59*time.Minute + 58*time.Second: "23h59m",
		15 * time.Minute:                               "15m",
		2*time.Minute + 30*time.Second:                 "2m30s",
		90 * time.Second:                               "1m30s",
		45 * time.Second:                               "45s",
		0:                                              "0s",
		1500 * time.Millisecond:                        "2s",
		-5 * time.Minute:                               "5m",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%v) = %q, want %q", d, got, want)
		}
	}
}

// One spelling of how long something has been up, in the two units a person
// steers by: a host that has been up a fortnight is "14d 2h", and nobody reads
// the minutes of it. Truncated and not rounded, since this counts time that has
// passed and 59m59s has not been an hour.
func TestUptimeIsTheTwoUnitsAPersonSteersBy(t *testing.T) {
	cases := map[time.Duration]string{
		14*24*time.Hour + 2*time.Hour + 59*time.Minute: "14d 2h",
		24 * time.Hour:                  "1d 0h",
		23*time.Hour + 59*time.Minute:   "23h 59m",
		time.Hour:                       "1h 0m",
		90 * time.Minute:                "1h 30m",
		59*time.Minute + 59*time.Second: "59m",
		time.Minute:                     "1m",
		45 * time.Second:                "45s",
		0:                               "0s",
		900 * time.Millisecond:          "0s",
		-time.Hour:                      "0s",
		365*24*time.Hour + 5*time.Hour:  "365d 5h",
		time.Duration(1<<63 - 1):        "106751d 23h",
		4*24*time.Hour + 19*time.Hour + 5*time.Minute: "4d 19h",
	}
	for d, want := range cases {
		if got := Uptime(d); got != want {
			t.Errorf("Uptime(%v) = %q, want %q", d, got, want)
		}
	}
}

// The days are in mid-July because clockAt reads them in the machine's own
// zone, and the table steps in whole days of 24 hours: across a change of
// the clocks one of those lands an hour off the time it was written for.
// Early September put Chile's spring forward inside the table, and under
// TZ=America/Santiago the day before read 05:20; no zone in the tz database
// moves its clocks between the days named here.
func TestClockSaysWhichDayOnceItIsNotToday(t *testing.T) {
	now := time.Date(2026, time.July, 12, 6, 20, 32, 0, time.Local)
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(15 * time.Minute), "06:35:32"},
		{now.Add(24 * time.Hour), "Mon 06:20:32"},
		{now.Add(5 * 24 * time.Hour), "Fri 06:20:32"},
		{now.Add(10 * 24 * time.Hour), "2026-07-22 06:20"},
		{now.Add(-24 * time.Hour), "2026-07-11 06:20"},
	}
	for _, c := range cases {
		if got := clockAt(c.at, now); got != c.want {
			t.Errorf("clockAt(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}
