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

func TestClockSaysWhichDayOnceItIsNotToday(t *testing.T) {
	now := time.Date(2026, time.September, 6, 6, 20, 32, 0, time.Local)
	cases := []struct {
		at   time.Time
		want string
	}{
		{now.Add(15 * time.Minute), "06:35:32"},
		{now.Add(24 * time.Hour), "Mon 06:20:32"},
		{now.Add(5 * 24 * time.Hour), "Fri 06:20:32"},
		{now.Add(10 * 24 * time.Hour), "2026-09-16 06:20"},
		{now.Add(-24 * time.Hour), "2026-09-05 06:20"},
	}
	for _, c := range cases {
		if got := clockAt(c.at, now); got != c.want {
			t.Errorf("clockAt(%v) = %q, want %q", c.at, got, c.want)
		}
	}
}
