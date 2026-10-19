package theme

import (
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// Grades is how much of the colour that grades is drawn: the `status:` entry of
// the `theme:` block, the one entry of it that is a choice and not a colour.
//
// **What a grade is.** A word or a number that says how well something is, and
// that a renderer draws in green, amber or red for it: a status word
// (ClassifyStatus), a usage percentage (ClassifyUsage), the border of a tile by
// the worst of what it says, the marker a folded section leaves in its header.
// Nothing else is one. A selection, a title, a key, a link, a diff, the dimming
// of an answer that is old, a consent request waiting for a person, an error and
// a notice are all coloured for what they are and not for how well anything is,
// and this setting leaves every one of them alone: it quiets a screen that is
// green from end to end, and it must not quiet a screen that is asking for
// something.
//
// **Safety and trust cues are not grades either** (Cue). The class of a call,
// read, write or destructive, says what it may do, and a plugin that is untrusted
// or failed to start says whether code can be relied on; neither says how well
// anything is. A red `destructive` that went quiet under "off" would be the wrong
// way for the setting to fail, so a cue is drawn in its colour in every mode, and
// every place that draws one asks Cue (StatusStyle does) instead of Grade.
//
// **Quieting is drawing in the plain style, never leaving out.** The word, the
// number, the border and the marker are all still there under every setting; only
// the colour of the ones that grade is taken away. What the grade *is* — which
// tile counts as bad, what a header says — is decided without colour (the TUI's
// tileGrade) and does not move.
type Grades int

const (
	// GradesAll draws every grade in its colour: green for ok, amber for warn, red
	// for bad. The setting when nothing states one, and what rta always drew.
	GradesAll Grades = iota
	// GradesAlerts draws only the two grades that ask for something. Ok is the
	// ordinary state and the one that fills a screen, so it is drawn plain; amber
	// and red are then the only colour of the grades, and each means something.
	GradesAlerts
	// GradesOff draws no grade in colour at all.
	GradesOff
)

// GradesKey is the entry of a `theme:` block that sets Grades. It sits among the
// colours because it is about them, and is none of them: Fields does not list it,
// and the palette's own colours never take it as theirs.
const GradesKey = "status"

var gradeNames = [...]string{GradesAll: "all", GradesAlerts: "alerts", GradesOff: "off"}

// String is the value as the config file spells it.
func (g Grades) String() string {
	if g < 0 || int(g) >= len(gradeNames) {
		return gradeNames[GradesAll]
	}
	return gradeNames[g]
}

// GradeModes are the values `status:` takes, in the order they quiet the screen.
func GradeModes() []string { return slices.Clone(gradeNames[:]) }

// ParseGrades is the setting a config value names, and false for anything that
// is not one of GradeModes exactly: a value is a choice among three words, and a
// near miss is reported with the three rather than guessed at.
func ParseGrades(s string) (Grades, bool) {
	for g, name := range gradeNames {
		if s == name {
			return Grades(g), true
		}
	}
	return GradesAll, false
}

// grades is the setting the palette holds, set with the colours by Apply and
// read when a grade is drawn.
var grades = GradesAll

// Grading is the setting in force.
func Grading() Grades { return grades }

// Coloured reports whether a grade of kind k is drawn in its colour under the
// setting in force. The question a renderer asks when the colour is not a style
// it can swap but a border or a marker it builds: a tile of kind StatusBad has
// a red border under "all" and "alerts" and a grey one under "off".
//
// A StatusMuted is never coloured by this: it recedes (Subtle) instead of
// standing out, which is what the setting is for, so flattening it to plain
// would make a closed or a done thing louder than it was.
func Coloured(k StatusKind) bool {
	switch k {
	case StatusGood:
		return grades == GradesAll
	case StatusWarn, StatusBad:
		return grades != GradesOff
	}
	return false
}

// Grade is the style a grade of kind k is drawn in: its status colour, the
// secondary text colour for a muted one, and plain when the setting quiets it or
// the word grades nothing. The one place the three status styles are chosen for a
// grade, so that no renderer decides the setting a second time.
func Grade(k StatusKind) lipgloss.Style {
	switch {
	case k == StatusMuted:
		return Subtle
	case !Coloured(k):
		return Plain
	default:
		return statusText(k)
	}
}

// statusText is the colour a kind of status has when it is drawn in one.
func statusText(k StatusKind) lipgloss.Style {
	switch k {
	case StatusGood:
		return GoodText
	case StatusWarn:
		return WarnText
	case StatusBad:
		return BadText
	}
	return Plain
}

// cues are the words drawn in a status colour that say what a call may do or
// whether code can be relied on, and the kind of colour each has: the safety class
// of a capability, and the two states of a plugin that is installed and not
// running, as `rta plugin list` and the plugin inventory say them.
var cues = map[string]StatusKind{
	"read": StatusGood, "write": StatusWarn, "destructive": StatusBad,
	"untrusted": StatusWarn, "failed to start": StatusBad,
}

// Cue is the style a safety or trust cue is drawn in, and whether s is one: the
// whole of the word, as the safety column of a table or the header of a result
// says it, and not a status that begins with it. A cue keeps its colour under
// every way of drawing status, because it is a statement about what a call may do
// or whether code is trusted, and not about how well anything is (Grades).
func Cue(s string) (lipgloss.Style, bool) {
	k, ok := cues[strings.ToLower(strings.TrimSpace(s))]
	if !ok {
		return Plain, false
	}
	return statusText(k), true
}
