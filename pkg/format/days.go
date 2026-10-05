package format

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// ParseWindow reads a window the way a person types one: Go's own spellings
// ("90m", "1h30m") and, in front of them, days — "1d", "2d", "1d12h" — which
// time.ParseDuration has no unit for and which is the first thing anybody
// asking "since yesterday" or "lock it for the weekend" reaches for.
//
// A day is twenty-four hours. A calendar day is not always, and a window a
// person means as "a day back" is read as the length of one rather than as a
// date: the two disagree twice a year, for an hour, and a duration that
// quietly depended on the clock changing would be a worse surprise than the
// hour.
//
// Only the leading unit is a day, so "12h1d" is refused rather than read both
// ways. A day count is held to what a Duration can carry, since a window that
// wrapped negative would be a lock that lifted itself before it was placed.
func ParseWindow(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	digits := 0
	for digits < len(raw) && (raw[digits] >= '0' && raw[digits] <= '9' || raw[digits] == '.') {
		digits++
	}
	if digits == 0 || digits >= len(raw) || raw[digits] != 'd' {
		return time.ParseDuration(raw)
	}
	days, err := strconv.ParseFloat(raw[:digits], 64)
	if err != nil {
		return 0, errors.New("not a window: " + raw)
	}
	const day = 24 * time.Hour
	if days > float64(time.Duration(1<<63-1)/day) {
		return 0, errors.New("a window that long does not fit: " + raw)
	}
	window := time.Duration(days * float64(day))
	if rest := raw[digits+1:]; rest != "" {
		more, err := time.ParseDuration(rest)
		if err != nil || more < 0 {
			return 0, errors.New("not a window: " + raw)
		}
		window += more
	}
	return window, nil
}
