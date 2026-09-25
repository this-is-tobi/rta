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
//
// It takes whatever integer type the caller is holding, because nothing in Go
// that measures bytes hands over a uint64: io.Copy returns int64, FileInfo.Size
// is int64, len is int, and a server's own stat is whatever its client library
// declared. While this took uint64 alone, every one of those callers wrote the
// widening itself — fifty-three sites across this repository and the plugins —
// and because gosec reads int64 -> uint64 as an overflow nothing has ruled out,
// five of them carried a //nolint repeating one sentence about one fact and
// three more clamped with max(n, 0) in case the sentence was wrong. Widening a
// byte count is this function's business, not the business of everybody who
// happens to have one.
//
// A negative count keeps its sign rather than wrapping into the unsigned
// range. Under the uint64 signature, a caller that subtracted its way below
// zero rendered -1 as "16.0 EiB" — a figure plausible enough for a reader to
// act on, which is worse than the "-1 B" that shows the bug.
func Bytes[T integer](n T) string {
	if n < 0 {
		// -(n+1)+1, not -n, which is still negative at T's own minimum.
		return "-" + binaryUnits(uint64(-(n+1))+1)
	}
	return binaryUnits(uint64(n))
}

// integer is every type a byte count turns up in. Unexported because a caller
// names the value, never the constraint.
type integer interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64
}

func binaryUnits(b uint64) string {
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
//
// The zero time reads "never": this is for a record's own timestamp, where
// zero is what a field holds before anything has happened. An instant
// somebody named — a token's exp, a date typed in — goes to Relative.
func Ago(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return Relative(t)
}

// Relative is Ago for an instant that is a value in its own right, where the
// zero time is 1 January of year 1 like any other date and not "unset": a
// token may carry it, and codec.jwt showed a token's year-1 iat as "never".
func Relative(t time.Time) string { return relativeTo(t, time.Now()) }

// farSpan is where a distance is counted in calendar years rather than as a
// Duration, a little inside the 292 years a Duration can hold. Past that,
// time.Since saturates: an exp of 9999-12-31 — the usual way to write "does
// not expire" — came back as the most negative Duration, negated into itself,
// and read "in -9223372036 seconds", and anything older than 1734 read "292
// years ago".
const farSpan = 290 * 365 * 24 * time.Hour

func relativeTo(t, now time.Time) string {
	d := now.Sub(t) // saturates rather than wraps, so the comparison holds
	switch {
	case d >= farSpan:
		return count(yearsBetween(t, now), "year") + " ago"
	case d <= -farSpan:
		return "in " + count(yearsBetween(now, t), "year")
	case d < 0:
		return "in " + span(-d)
	case d < time.Second:
		return "just now"
	}
	return span(d) + " ago"
}

// yearsBetween counts the calendar years from from to to, rounded to the
// nearest the way span rounds within its unit. Only the part under a year is
// a Duration, which cannot overflow.
func yearsBetween(from, to time.Time) int {
	n := to.Year() - from.Year()
	if from.AddDate(n, 0, 0).After(to) {
		n--
	}
	if to.Sub(from.AddDate(n, 0, 0)) >= 365*24*time.Hour/2 {
		n++
	}
	return n
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
