// Package theme is the single source of truth for styling across every
// renderer. Sober but warm: one identity color, one sparing
// accent, semantic state colors, and — the detail that makes structure
// recede and content pop — two distinct grays: Faint for chrome (borders,
// fills) and Muted for secondary text. No other renderer defines colors.
//
// One counterweight to the warm identity: Label, for the name of a pane. It
// exists because a panel title and the keys inside that panel were both
// Primary, so a tile announced itself in exactly the colour of its own
// contents and the eye had nothing to separate them by.
//
// Colors are truecolor and degrade automatically: bubbletea v2 downsamples
// TUI output to the terminal's profile, and the CLI wraps stdout/stderr in a
// colorprofile writer. On 256-color terminals every shade has a near match.
//
// The palette is operator-overridable through Apply, called once at startup
// with whatever config.Config.Theme holds. Every var below stays exactly what
// it was: a package a renderer reads without knowing whether the value under
// it is the built-in or something an operator wrote down.
package theme

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// Built-in palette, as hex — the one place each value is written down.
// lipgloss.Color(s string) color.Color is a constructor, not a defined type
// over string, so the var block below cannot itself hold a color and answer
// "what hex was that" later; Apply and Current both need the string form, so
// it is named here and the color is built from it, once.
const (
	primaryHex = "#D97757" // clay orange — identity, keys, selection
	accentHex  = "#F0A868" // amber — sparing highlights (filter matches)
	mutedHex   = "#8B8B96" // secondary text
	faintHex   = "#4A4A56" // structure: borders, chart fills
	// labelHex is Label's color, chosen against four constraints rather than
	// by eye: hue 212° against Primary's 15°, near enough opposite that a
	// title can never read as content; calibrated to Primary's exact relative
	// luminance (0.286 both), so it sits beside the orange as a peer instead
	// of shouting over it or fading under it, and keeps Primary's 3.12
	// contrast on a light terminal — the 3.0 floor for bold text, which every
	// brighter candidate fell through; only 30% saturated, so it stays
	// subordinate to the content it names; and 57° from Good, the nearest
	// status hue, since tile bodies are full of coloured statuses and a title
	// that reads as one is worse than a title that reads as a key.
	labelHex   = "#7794B6" // slate blue — pane names
	goodHex    = "#3ED598"
	warnHex    = "#FFC24B"
	badHex     = "#FF6B7A"
	inverseHex = "#FFFFFF"
	inkHex     = "#1A1A22"
)

// Palette.
var (
	Primary = lipgloss.Color(primaryHex)
	Accent  = lipgloss.Color(accentHex)
	Muted   = lipgloss.Color(mutedHex)
	Faint   = lipgloss.Color(faintHex)
	Label   = lipgloss.Color(labelHex)
	Good    = lipgloss.Color(goodHex)
	Warn    = lipgloss.Color(warnHex)
	Bad     = lipgloss.Color(badHex)
	Inverse = lipgloss.Color(inverseHex)
	Ink     = lipgloss.Color(inkHex)
)

// Shared styles, built once at package init and again by rebuildStyles
// whenever Apply changes the palette underneath them.
//
// Declared at their zero value rather than derived inline, because a var
// initializer runs exactly once — before main, before config is loaded — and
// an operator override needs the same derivation to run again afterwards.
// One function is the whole difference between "computed at startup" and
// "computed from whatever the palette holds right now"; two copies of the
// seven lines below, one inline and one in rebuildStyles, would have been the
// usual way for an override to reach nine of the thirteen styles and miss
// PanelTitle, because nothing would have forced the two to agree.
var (
	Key        lipgloss.Style
	Header     lipgloss.Style
	Border     lipgloss.Style
	Subtle     lipgloss.Style
	Faded      lipgloss.Style
	Title      lipgloss.Style
	PanelTitle lipgloss.Style
	AccentTxt  lipgloss.Style
	GoodText   lipgloss.Style
	WarnText   lipgloss.Style
	BadText    lipgloss.Style
	ErrBadge   lipgloss.Style
	HintBadge  lipgloss.Style
)

// rebuildStyles derives every shared style from the palette's current
// values. The one place that derivation is written, so init and Apply cannot
// disagree about what a style means.
func rebuildStyles() {
	Key = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	Header = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	Border = lipgloss.NewStyle().Foreground(Faint)
	Subtle = lipgloss.NewStyle().Foreground(Muted)
	Faded = lipgloss.NewStyle().Foreground(Faint)
	Title = lipgloss.NewStyle().Foreground(Primary).Bold(true)
	// PanelTitle is the name in a panel's top border. Applied by panel()
	// itself, never by its callers — see internal/render/tui/panel.go.
	PanelTitle = lipgloss.NewStyle().Foreground(Label).Bold(true)
	AccentTxt = lipgloss.NewStyle().Foreground(Accent)
	GoodText = lipgloss.NewStyle().Foreground(Good)
	WarnText = lipgloss.NewStyle().Foreground(Warn)
	BadText = lipgloss.NewStyle().Foreground(Bad)
	ErrBadge = lipgloss.NewStyle().Foreground(Inverse).Background(Bad).Bold(true).Padding(0, 1)
	HintBadge = lipgloss.NewStyle().Foreground(Ink).Background(Warn).Bold(true).Padding(0, 1)
}

// Plain is the zero style, used to switch styling off wholesale.
var Plain = lipgloss.NewStyle()

// entry is one overridable palette slot: the var it controls, the hex it
// resets to when an operator does not override it, and the hex it currently
// holds — tracked separately from the var itself because lipgloss.Color's
// return type carries no way to ask a color what hex built it.
type entry struct {
	ptr     *color.Color
	builtin string
	hex     string
}

// set writes v to both the palette var and the entry's own record, so the
// two can never show two different answers to "what is primary right now".
func (e *entry) set(v string) {
	*e.ptr = lipgloss.Color(v)
	e.hex = v
}

// fields maps a config key to the palette var it controls.
var fields = map[string]*entry{
	"primary": {ptr: &Primary, builtin: primaryHex},
	"accent":  {ptr: &Accent, builtin: accentHex},
	"muted":   {ptr: &Muted, builtin: mutedHex},
	"faint":   {ptr: &Faint, builtin: faintHex},
	"label":   {ptr: &Label, builtin: labelHex},
	"good":    {ptr: &Good, builtin: goodHex},
	"warn":    {ptr: &Warn, builtin: warnHex},
	"bad":     {ptr: &Bad, builtin: badHex},
	"inverse": {ptr: &Inverse, builtin: inverseHex},
	"ink":     {ptr: &Ink, builtin: inkHex},
}

func init() {
	for _, e := range fields {
		e.hex = e.builtin
	}
	rebuildStyles()
}

// Fields lists the config keys Apply understands, sorted.
func Fields() []string {
	out := make([]string, 0, len(fields))
	for k := range fields {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HexColor is the one shape Apply accepts: truecolor, the same as every
// built-in value above. Named colors and ANSI codes are not offered — one
// format that always degrades correctly (package doc, above) is worth more
// than a second one that sometimes needs a terminal profile to interpret.
//
// Exported so a config editor validates a color exactly the rule Apply
// enforces — a second regexp typed out beside this one is a second place a
// hex pattern could be fixed and the other forgotten.
var HexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Problem is one stated override Apply could not honour, and why. Shaped
// like internal/pluginconf.Problem on purpose — same question, "what did the
// operator write that this could not use" — without theme importing
// pluginconf or being imported by it: nothing about color needs to know
// about plugins, and nothing about plugins needs to know about color.
type Problem struct {
	Field, Reason, Hint string
}

func (p Problem) String() string {
	if p.Hint == "" {
		return fmt.Sprintf("theme.%s: %s", p.Field, p.Reason)
	}
	return fmt.Sprintf("theme.%s: %s (%s)", p.Field, p.Reason, p.Hint)
}

// Apply sets the palette from overrides, keyed the way Fields lists them,
// each a "#rrggbb" string. Call once, at startup, before anything renders —
// and safe to call again: every field resets to its built-in first, so a
// second call with a different (or empty) map is a fresh decision rather
// than one layered on the last.
//
// An unknown key or a malformed value is reported, not fatal: the field it
// named keeps its built-in color and every other field is unaffected, the
// same "one bad entry costs its own line, not the page" rule
// internal/pluginconf.Resolve already applies to plugin sections.
func Apply(overrides map[string]string) []Problem {
	for _, e := range fields {
		e.set(e.builtin)
	}

	var problems []Problem
	names := make([]string, 0, len(overrides))
	for k := range overrides {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := overrides[k]
		e, known := fields[k]
		switch {
		case !known:
			problems = append(problems, Problem{Field: k,
				Reason: "not a color this rta understands",
				Hint:   "one of: " + strings.Join(Fields(), ", ")})
		case !HexColor.MatchString(v):
			problems = append(problems, Problem{Field: k,
				Reason: fmt.Sprintf("%q is not a color", v),
				Hint:   "the form is #rrggbb, e.g. #D97757"})
		default:
			e.set(v)
		}
	}
	rebuildStyles()
	return problems
}

// Current returns every field's live value, hex-formatted — what a config
// editor seeds its form with, and what Apply's own tests reset against.
func Current() map[string]string {
	out := make(map[string]string, len(fields))
	for k, e := range fields {
		out[k] = e.hex
	}
	return out
}

// StatusKind classifies a status string for semantic coloring.
type StatusKind int

const (
	StatusNeutral StatusKind = iota
	StatusGood
	StatusWarn
	StatusBad
	StatusMuted
)

// ClassifyStatus maps common status vocabulary to a semantic kind. Renderers
// use it to color KindStatus cells; the vocabulary is deliberately small and
// grows only with real usage.
//
// **An unrecognised word is Neutral, which means it renders with no colour at
// all** — and that failed quietly in the worst direction. `sys load` graded
// itself "overloaded"/"busy" and `eol` graded a dead release "EOL": three
// words this list did not know, so the most severe state each function could
// report was the one drawn plainest, right beside an "ok" in green. The words
// are the right words to show a person, so the list learned them rather than
// the callers learning to say "fail overloaded".
//
// TestEveryStatusWordTheBuiltinsUseIsColoured is what keeps this honest: it
// carries the literals the plugins actually emit, so adding a fourth unknown
// word fails a test instead of silently losing a colour.
func ClassifyStatus(s string) StatusKind {
	v := strings.ToLower(strings.TrimSpace(s))
	switch {
	case v == "":
		return StatusNeutral
	case strings.HasPrefix(v, "ok") || strings.HasPrefix(v, "valid") ||
		strings.HasPrefix(v, "open") || strings.HasPrefix(v, "up") ||
		strings.HasPrefix(v, "active") || strings.HasPrefix(v, "read"):
		return StatusGood
	case strings.HasPrefix(v, "warn") || strings.HasPrefix(v, "write") ||
		strings.HasPrefix(v, "pending") || strings.HasPrefix(v, "busy") ||
		// A plugin found on $PATH and not run. Amber rather than red: nothing
		// is wrong, a decision is outstanding — and rather than neutral,
		// because "installed and doing nothing" is the state a trust gate has
		// to make impossible to miss.
		strings.HasPrefix(v, "untrusted"):
		return StatusWarn
	case strings.HasPrefix(v, "error") || strings.HasPrefix(v, "expired") ||
		strings.HasPrefix(v, "invalid") || strings.HasPrefix(v, "fail") ||
		strings.HasPrefix(v, "denied") || strings.HasPrefix(v, "unreachable") ||
		strings.HasPrefix(v, "destructive") || strings.HasPrefix(v, "down") ||
		strings.HasPrefix(v, "overloaded") || strings.HasPrefix(v, "eol") ||
		strings.HasPrefix(v, "critical"):
		return StatusBad
	case strings.HasPrefix(v, "closed") || strings.HasPrefix(v, "info") ||
		strings.HasPrefix(v, "none") || strings.HasPrefix(v, "disabled") ||
		strings.HasPrefix(v, "done"):
		return StatusMuted
	default:
		return StatusNeutral
	}
}

// ClassifyUsage grades one view.KindUsage cell — how full something is, from
// the text a producer wrote there.
//
// **From the text, because that is all a view carries.** Views hold
// pre-formatted strings by contract, so a percentage arrives as "88%" or
// "94.2%" depending on which producer wrote it, and neither the number nor
// the capacity behind it survives the crossing. Parsing it back is the only
// thing a renderer can do, and getting it wrong in the quiet direction is the
// safe one: anything that does not read as a number is Neutral, which is no
// colour rather than a wrong one. That is what an unmeasurable cell — a pod
// with no memory limit, a node whose kubelet did not answer — already looks
// like, and it is written as "" or "—" rather than as a number.
//
// Above 100 stays Bad rather than becoming a case of its own. A KindUsage
// column is a capacity, so past it is the worst thing it can say; a column
// where 150 is ordinary is one of the several that must not declare this kind
// at all, and view.KindUsage's own comment names them.
func ClassifyUsage(cell string) StatusKind {
	v := strings.TrimSuffix(strings.TrimSpace(cell), "%")
	pct, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return StatusNeutral
	}
	switch {
	case pct >= view.UsageBad:
		return StatusBad
	case pct >= view.UsageWarn:
		return StatusWarn
	default:
		return StatusGood
	}
}

// UsageStyle returns the style for a usage cell.
func UsageStyle(cell string) lipgloss.Style {
	switch ClassifyUsage(cell) {
	case StatusGood:
		return GoodText
	case StatusWarn:
		return WarnText
	case StatusBad:
		return BadText
	default:
		return Plain
	}
}

// Badge renders text as a filled block in a colour chosen outside the palette
// — today, the one an operator gave a profile.
//
// **The ink is computed rather than picked, and that is the whole of why this
// is a function.** A profile colour is written by hand and can be anything:
// white on #FFC24B is unreadable and dark on #1A1A22 is invisible, and either
// failure lands on the single label whose entire job is to be impossible to
// miss. WCAG relative luminance decides, at the 0.179 threshold that is the
// crossover where black and white contrast equally against a background —
// the same question a browser answers to pick readable text on an arbitrary
// swatch.
//
// A colour that is not a colour returns the text unchanged rather than
// half-styled. A profile with no colour is the ordinary case and not an error,
// and a badge painted in a default would say "this environment is marked" about
// one that is not.
func Badge(text, hex string) string {
	if !HexColor.MatchString(hex) {
		return text
	}
	ink := inverseHex
	if relativeLuminance(hex) > 0.179 {
		ink = inkHex
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color(hex)).
		Foreground(lipgloss.Color(ink)).
		Bold(true).
		Padding(0, 1).
		Render(text)
}

// relativeLuminance is WCAG 2.x for a #rrggbb string: each channel
// gamma-expanded, then weighted by how much the eye owes it. Green carries
// most of the answer, which is why a mid green background needs dark ink and a
// mid blue of the same nominal brightness does not.
//
// Callers have already matched HexColor, so the parse cannot fail; a channel
// that somehow does not read counts as black, which errs toward light ink on a
// colour nothing knows — legible more often than the other way round.
func relativeLuminance(hex string) float64 {
	return 0.2126*channel(hex[1:3]) + 0.7152*channel(hex[3:5]) + 0.0722*channel(hex[5:7])
}

func channel(pair string) float64 {
	v, err := strconv.ParseUint(pair, 16, 8)
	if err != nil {
		return 0
	}
	c := float64(v) / 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

// StatusStyle returns the style for a status string.
func StatusStyle(s string) lipgloss.Style {
	switch ClassifyStatus(s) {
	case StatusGood:
		return GoodText
	case StatusWarn:
		return WarnText
	case StatusBad:
		return BadText
	case StatusMuted:
		return Subtle
	default:
		return Plain
	}
}
