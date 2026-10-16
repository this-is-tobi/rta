package config

import (
	"fmt"
	"strings"
	"time"
)

// MaxRefresh is the longest a tile can be asked to wait between runs. A tile
// that runs once a day is a report and not a tile, and a bound turns a typo
// (`refresh: 3600h`) into a refusal instead of a tile that never moves.
const MaxRefresh = 24 * time.Hour

// ParseRefresh reads a `refresh:` — `30s`, `5m`, `1h30m` — wherever one is
// written: on a tile, on a section, on the screen. One reader for all three, so
// that what is valid in one place is valid in the others and says the same thing
// when it is not.
//
// Nothing written is zero and no error, which is every level's "inherit". What
// is refused is a length of time with no unit (`30`), which is a number of
// something and not of seconds; zero or less, which is no pace at all; and more
// than MaxRefresh. The floor under a value, the timer's own tick and a
// capability's declared pace, is not this function's to know: it is applied
// where a capability is in hand (plugin.ClampPace) and reported by
// `rta config check` where a person can still change the line.
func ParseRefresh(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(text)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%q is not a length of time — write it with a unit: 30s, 5m, 1h", text)
	case d <= 0:
		return 0, fmt.Errorf("%q is not a pace: it takes a length of time above zero, such as 30s", text)
	case d > MaxRefresh:
		return 0, fmt.Errorf("%q is more than a tile can wait between runs (at most %s)", text, MaxRefresh)
	}
	return d, nil
}

// FormatRefresh is a pace as it is written back to a file and read on a list:
// `30s`, `5m`, `1h30m`, without the zero units a duration prints with.
func FormatRefresh(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
}
