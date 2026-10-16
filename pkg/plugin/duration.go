package plugin

import (
	"regexp"
	"time"

	"github.com/this-is-tobi/rta/pkg/format"
)

// A Duration input is a length of time written with its unit: "30s", "5m",
// "2h", "1d".
//
// **Carried as text everywhere, and that is the design.** An Int named
// `timeout` says nothing about whether it counts seconds or milliseconds, and
// the answer lived in a Help string, so `--timeout 30` meant 30 seconds in one
// capability, 30 milliseconds in another and 30 minutes in a third. A number
// with a unit attached cannot be misread, and text is what the CLI types, an
// agent sends, a form edits and a config file holds, with no conversion on
// the way for one of those to get wrong. The wire carries a string; the
// handler reads a time.Duration (Request.Duration).
//
// A bare number is refused rather than read as seconds: the host would be
// guessing for the plugin that meant milliseconds, and "30" is the one
// spelling every reader has a different default for.

// ParseDuration reads a length of time as a Duration input is written: a
// sequence of numbers each followed by its unit, "1h30m" or "2d" or "1.5h". It is
// format.ParseDuration, the one grammar every place that takes a time reads, kept
// here so that the host and the handler agree on what a value means.
func ParseDuration(s string) (time.Duration, error) { return format.ParseDuration(s) }

// FormatDuration is d written the way a person writes one: whole days as "2d",
// otherwise Go's spelling without the zero units it pads with. What ParseDuration
// reads back as d.
func FormatDuration(d time.Duration) string { return format.FormatDuration(d) }

// toDuration reads v as a Duration input's value: the text it is declared and
// sent as, or a time.Duration an in-process caller built. Nothing else — a
// number is refused, so a value whose unit nobody wrote is never guessed at.
func toDuration(v any) (time.Duration, bool) {
	switch d := v.(type) {
	case string:
		parsed, err := ParseDuration(d)
		return parsed, err == nil
	case time.Duration:
		return d, true
	}
	return 0, false
}

// bareNumber reports whether v is a number nobody gave a unit: a numeric value
// as a config file or an agent's JSON carries one, or the digits a command-line
// flag hands over for it, since every flag arrives as text. It is the one
// mistake in writing a duration common enough to be named as what it is.
func bareNumber(v any) bool {
	if text, isText := v.(string); isText {
		return bareNumberText.MatchString(text)
	}
	return statedShape(v) == "a number"
}

var bareNumberText = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?$`)

// Duration reads a Duration input. An input nobody gave, and one that is not a
// duration, read as zero, as Int does: the host has already refused the second
// (CheckInputs) before a handler runs.
func (r Request) Duration(name string) time.Duration {
	d, _ := toDuration(r.values[name])
	return d
}

// DurationPattern is the grammar ParseDuration reads, as the regular expression a
// JSON Schema carries for a string (format.DurationPattern).
const DurationPattern = format.DurationPattern

// durationHint is how a refusal says to write one.
const durationHint = "write it with a unit: 30s, 5m, 2h or 1d"
