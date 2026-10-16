package format

import (
	"errors"
	"strings"
	"time"
)

// ParseWindow reads a window the way a person types one: ParseDuration's grammar
// — "90m", "1h30m", "2d", "1w" — with the optional sign a "since" carries
// ("-2h" is two hours back) and the spaces a flag value may have around it.
//
// It was once its own parser, with days in front of Go's grammar, which is how
// a lock took `1d` while a profile's deadline did not. It is ParseDuration now,
// so there is one answer to what a length of time looks like; the only thing it
// adds is the sign, because a window back from now is the one place a negative
// length is meant.
//
// A day is twenty-four hours. A calendar day is not always, and a window a person
// means as "a day back" is read as the length of one rather than as a date: the
// two disagree twice a year, for an hour, and a duration that quietly depended on
// the clock changing would be a worse surprise than the hour.
func ParseWindow(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	negative := strings.HasPrefix(raw, "-")
	if negative || strings.HasPrefix(raw, "+") {
		raw = raw[1:]
	}
	d, err := ParseDuration(raw)
	if err != nil {
		return 0, errors.New("not a window: " + raw)
	}
	if negative {
		return -d, nil
	}
	return d, nil
}
