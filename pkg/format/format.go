// Package format holds the formatting vocabulary a view producer needs.
//
// A view carries pre-formatted strings (pkg/view contract): view.ColumnKind
// selects alignment and styling, never a number's rendering, so whoever
// builds the view formats it. This package exists so that everybody who has
// to do that says the same thing.
//
// In pkg rather than internal, because "everybody" is mostly not in this
// repository. It lived under internal while the only producers were built-in,
// which left every external plugin held to a contract whose vocabulary it
// could not import — so the first one to show a byte count showed
// `1392640`.
package format

import (
	"fmt"
	"time"
)

// Bytes renders a byte count in binary units: "512 B", "2.0 KiB", "3.0 GiB".
func Bytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// Ago renders how long ago something happened, in the one unit that answers
// the question: "12 seconds ago", "3 hours ago", "2 weeks ago".
//
// One unit, not two. A timestamp on a dashboard is read to answer "is this
// recent?", and "2 weeks, 3 days, 4 hours and 11 seconds ago" answers it worse
// than "2 weeks ago" while taking four times the width. Rounded within that
// unit rather than truncated, for the same reason: 90 seconds is "2 minutes
// ago" to a person and "1 minute ago" only to a computer.
//
// A time in the future says so rather than reading as a very old one, because
// clock skew between a machine and whatever stamped the record is ordinary and
// "in 3 minutes" is a fact worth seeing.
func Ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	if d < 0 {
		return "in " + span(-d)
	}
	if d < time.Second {
		return "just now"
	}
	return span(d) + " ago"
}

// span is Ago's magnitude half, without the direction.
func span(d time.Duration) string {
	switch {
	case d < time.Minute:
		return count(int(d.Round(time.Second)/time.Second), "second")
	case d < time.Hour:
		return count(int(d.Round(time.Minute)/time.Minute), "minute")
	case d < 24*time.Hour:
		return count(int(d.Round(time.Hour)/time.Hour), "hour")
	case d < 7*24*time.Hour:
		return count(int(d.Round(24*time.Hour)/(24*time.Hour)), "day")
	case d < 365*24*time.Hour:
		return count(int(d.Round(7*24*time.Hour)/(7*24*time.Hour)), "week")
	}
	return count(int(d.Round(365*24*time.Hour)/(365*24*time.Hour)), "year")
}

func count(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
