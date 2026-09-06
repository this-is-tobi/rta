package format

import (
	"fmt"
	"time"
)

// Duration renders a window the way a person types one — "24h", "1h30m",
// "15m", "90s" — rather than the way time.Duration prints itself, which is
// "24h0m0s" and "15m0s": accepted by ParseDuration and typed by nobody.
//
// Two units at most, and seconds drop out once a window reaches an hour.
// Ago answers "is this recent?" with one unit; this answers "how long do I
// have?", where 1h30m and 2m30s are the difference between two plans, but
// nobody with hours left is steering by the seconds — and "23h59m58s" is a
// column that never stops changing.
func Duration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	d = d.Round(time.Second)
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	switch {
	case h > 0 && m == 0:
		return fmt.Sprintf("%dh", h)
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	case m > 0 && s == 0:
		return fmt.Sprintf("%dm", m)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// Clock renders a moment for a sentence that says "until": the time of day
// alone while that day is today, the weekday in front of it inside the
// week, the date past that. A day-long grant issued at 06:20 used to read
// "until 06:20:32", which at 06:20 reads as a grant that has already ended.
func Clock(t time.Time) string {
	return clockAt(t, time.Now())
}

func clockAt(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y, m, d := t.Date()
	ny, nm, nd := now.Date()
	if y == ny && m == nm && d == nd {
		return t.Format("15:04:05")
	}
	if t.After(now) && t.Sub(now) < 6*24*time.Hour {
		return t.Format("Mon 15:04:05")
	}
	return t.Format("2006-01-02 15:04")
}
