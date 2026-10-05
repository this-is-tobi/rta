package cli

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/guptarohit/asciigraph"
)

// ASCIIOnly reports whether pretty output should be drawn with ASCII alone:
// the locale is not UTF-8, or the terminal is one that cannot draw a rounded
// corner.
//
// A session over an old ssh, a CI log and a legacy console under LC_ALL=C all
// got box-drawing characters as mojibake, with no way to say "plain" short of
// -o md. Deciding it here, from the two variables that already say what the
// terminal can display, is what keeps it out of the configuration: there is no
// key for it, and the way to get the boxes back is the way every other tool
// takes, a UTF-8 locale (LC_ALL=C.UTF-8).
//
// The locale is read as POSIX reads it: LC_ALL, then LC_CTYPE, then LANG, the
// first of them that is set deciding. **None set decides nothing, and that is
// UTF-8 here.** A container or `env -i` with no locale at all is the C locale
// to a C program, and an ASCII table there would be right for a console that
// cannot draw the box and wrong for the terminal emulator nearly every such
// shell is typed in, which all draw it. A locale somebody wrote down is a
// statement about their terminal; the absence of one is not.
//
// TERM=linux is the kernel console, whose font has no rounded corners, and
// TERM=dumb is a terminal that cannot place the cursor, which the bare command
// already answers with help instead of a screen.
func ASCIIOnly(env func(name string) string) bool {
	switch env("TERM") {
	case "linux", "dumb":
		return true
	}
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if locale := env(name); locale != "" {
			return !utf8Locale(locale)
		}
	}
	return false
}

// utf8Locale reports whether a locale name such as en_US.UTF-8, C.utf8 or
// de_DE.UTF-8@euro names UTF-8. A bare "UTF-8" does too, which is how macOS's
// Terminal sets LC_CTYPE.
func utf8Locale(locale string) bool {
	charset := locale
	if _, after, found := strings.Cut(locale, "."); found {
		charset = after
	}
	charset, _, _ = strings.Cut(charset, "@")
	return strings.EqualFold(strings.ReplaceAll(charset, "-", ""), "utf8")
}

// glyphs are the characters the renderer draws structure with, as opposed to
// the text it is handed. Text is never rewritten: a value with an accent in it
// is the capability's, and a terminal that shows it wrongly is showing it
// wrongly in either mode.
type glyphs struct {
	border lipgloss.Border
	// rule is the heading band a section draws to the width of the terminal.
	rule string
	// join separates the parts of a footer and a chart's legend; dash a message
	// from the hint that follows it.
	join, dash string
	// branch, last, trunk and gap are a tree's connectors, each four cells.
	branch, last, trunk, gap string
	// full and empty are a bar's two halves.
	full, empty string
	// marker points at the selected record, and is as wide as its absence.
	marker string
	// plot is what a line chart's series are drawn with, and axis the ticks
	// down its left edge, which asciigraph draws with characters of its own
	// that no option reaches: the plain one when axis is ASCII (see
	// asciiAxis).
	plot asciigraph.CharSet
	axis *strings.Replacer
}

var unicodeGlyphs = glyphs{
	border: lipgloss.RoundedBorder(),
	rule:   "─",
	join:   " · ",
	dash:   " — ",
	branch: "├── ", last: "└── ", trunk: "│   ", gap: "    ",
	full: "█", empty: "░",
	marker: "▸ ",
	plot:   asciigraph.DefaultCharSet,
}

var asciiGlyphs = glyphs{
	border: lipgloss.ASCIIBorder(),
	rule:   "-",
	join:   " - ",
	dash:   " - ",
	branch: "|-- ", last: "`-- ", trunk: "|   ", gap: "    ",
	full: "#", empty: ".",
	marker: "> ",
	plot: asciigraph.CharSet{
		Horizontal: "-", VerticalLine: "|",
		ArcDownRight: "+", ArcDownLeft: "+", ArcUpRight: "+", ArcUpLeft: "+",
		EndCap: "-", StartCap: "-", UpRight: "+", DownHorizontal: "+",
	},
	axis: strings.NewReplacer("┤", "|", "┼", "+"),
}

// asciiAxis is a line chart with the two axis characters asciigraph hard-codes
// swapped for plain ones, on every line but the caption: that one is the
// capability's series names, which are never rewritten.
func (g glyphs) asciiAxis(plot string) string {
	if g.axis == nil {
		return plot
	}
	lines := strings.Split(plot, "\n")
	for i := range lines[:max(len(lines)-1, 0)] {
		lines[i] = g.axis.Replace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func glyphsFor(ascii bool) glyphs {
	if ascii {
		return asciiGlyphs
	}
	return unicodeGlyphs
}
