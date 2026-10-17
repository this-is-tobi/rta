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
// The palette is operator-overridable through Apply, which takes the colours
// of one block, and through ApplyLayers, which folds the blocks that apply while
// an environment is in force: the top-level `theme:`, the environment's colour
// and its own `theme:`. It is called at startup and again whenever the
// environment changes, so nothing may keep a colour it computed earlier — every
// var below stays exactly what it was, a package a renderer reads without
// knowing whether the value under it is the built-in or something an operator
// wrote down, and a style derived from one is derived again (rebuildStyles).
package theme

import (
	"fmt"
	"image/color"
	"math"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/near"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Built-in palette, as hex — the one place each value is written down.
// lipgloss.Color(s string) color.Color is a constructor, not a defined type
// over string, so the var block below cannot itself hold a color and answer
// "what hex was that" later; Apply and Current both need the string form, so
// it is named here and the color is built from it, once.
const (
	primaryHex = "#D97757" // clay orange — identity, keys, selection
	// The status colours and the accent sit at relative luminance 0.26, which
	// is 3.4 against white and 6.2 against black. They were brighter, and a
	// "warn" that was 13 against black was 1.6 against white: a light terminal
	// showed "ok" and "warn" as near-white text on white. One palette that
	// reads on both is worth more than a pretty dark one plus a guess at which
	// terminal this is, and detecting it is a query some terminals never
	// answer. Hue and saturation are the old ones, so a status still reads as
	// the colour it was.
	accentHex = "#D77015" // amber — sparing highlights (filter matches)
	mutedHex  = "#8B8B96" // secondary text
	faintHex  = "#4A4A56" // structure: borders, chart fills
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
	goodHex    = "#229F6C"
	warnHex    = "#BF7E00"
	badHex     = "#FF4357"
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
	// Source is the config key of the block the entry was written in, "" for
	// the top-level `theme:` — the one Apply reads — and `profiles.prod.theme`
	// for an environment's, so a line in a report names the line to open.
	Source string
	// Field is the key as it was written: a palette slot, or `color` for the
	// colour a profile gives its badge.
	Field, Reason, Hint string
}

// Key is the entry as the config file spells it: `theme.primary`,
// `profiles.prod.theme.bad`, `profiles.prod.color`.
func (p Problem) Key() string {
	source := p.Source
	if source == "" {
		source = "theme"
	}
	if p.Field == "" {
		return source
	}
	return source + "." + p.Field
}

func (p Problem) String() string {
	if p.Hint == "" {
		return fmt.Sprintf("%s: %s", p.Key(), p.Reason)
	}
	return fmt.Sprintf("%s: %s (%s)", p.Key(), p.Reason, p.Hint)
}

// Apply sets the palette from overrides, keyed the way Fields lists them,
// each a "#rrggbb" string. Called at startup, before anything renders, and
// again whenever the environment in force changes: every field resets to its
// built-in first, so a second call with a different (or empty) map is a fresh
// decision rather than one layered on the last.
//
// An unknown key or a malformed value is reported, not fatal: the field it
// named keeps its built-in color and every other field is unaffected, the
// same "one bad entry costs its own line, not the page" rule
// internal/pluginconf.Resolve already applies to plugin sections.
//
// This is the whole palette as one block states it. A palette that depends on
// which environment is in force is Resolve's, over the layers that make it up.
func Apply(overrides map[string]string) []Problem {
	usable, problems := validate("", "", overrides)
	for _, e := range fields {
		e.set(e.builtin)
	}
	for k, v := range usable {
		fields[k].set(v)
	}
	rebuildStyles()
	return problems
}

// validate is the one place an entry is judged: the colours of a block that a
// palette can use, and a problem for each it cannot. as names the key a single
// colour was written under when that is not the slot's own — a profile's
// `color:` is the primary slot — and is empty for a block of slots.
func validate(source, as string, colors map[string]string) (map[string]string, []Problem) {
	names := make([]string, 0, len(colors))
	for k := range colors {
		names = append(names, k)
	}
	sort.Strings(names)
	usable := make(map[string]string, len(colors))
	var problems []Problem
	for _, k := range names {
		v := colors[k]
		written := k
		if as != "" {
			written = as
		}
		_, known := fields[k]
		switch {
		case !known:
			// The guess is in the reason and not the hint: a note on a profile's pane
			// is cut to its reason, and the mistake it exists to catch is a name
			// one letter or one case from the right one.
			reason := "not a color this rta understands"
			if guess := NearestSlot(k); guess != "" {
				reason += " — did you mean `" + guess + "`?"
			}
			problems = append(problems, Problem{Source: source, Field: written,
				Reason: reason, Hint: "one of: " + strings.Join(Fields(), ", ")})
		// **An unquoted # is the likeliest mistake in a colour, and it arrives as
		// nothing.** `label: #C792EA` is a label with a comment after it, which
		// YAML reads as no value at all, and "\"\" is not a color" tells a person
		// they wrote an empty string they did not write.
		case strings.TrimSpace(v) == "":
			problems = append(problems, Problem{Source: source, Field: written,
				Reason: "has no value — a # starts a YAML comment, so an unquoted color reads as nothing",
				Hint:   "quote it: " + quotedExample(written)})
		case !HexColor.MatchString(v):
			problems = append(problems, Problem{Source: source, Field: written,
				Reason: fmt.Sprintf("%q is not a color", v),
				Hint:   "the form is #rrggbb, quoted in the file: " + quotedExample(written)})
		default:
			usable[k] = v
		}
	}
	return usable, problems
}

// quotedExample is a line that works, for a hint to show: the key as it was
// written, and a colour in quotes, which is the form that survives YAML.
func quotedExample(key string) string { return key + `: "#D97757"` }

// NearestSlot is the palette slot a mistyped name was most likely meant to be,
// or "" when none is near enough to say. Slot names are lower case only, so a
// name in another case is the slot it differs from by nothing else.
//
// Here and not beside the command that first offered it, since the same
// mistake is made in the file, where it is read by a check and a doctor with
// nobody to ask, and in an editor's note: one answer for every place a slot is
// named.
func NearestSlot(name string) string { return near.Word(name, Fields()) }

// Layer is one place colours are written down: a block of the config file.
type Layer struct {
	// Source is the config key of the block, as a Problem names it: "theme",
	// "profiles.prod.theme". Empty is the top-level block.
	Source string
	Colors map[string]string
	// As is the key a single colour was written under, when it is not the
	// slot's own: a profile's `color:` is the primary slot and says so here, so
	// a report names the line in the file and not a slot the file does not have.
	As string
	// Guarded marks a layer an environment brings with it, or a file nobody
	// named. The palette refuses of a guarded layer what it would not refuse of
	// the operator's own block: see Resolve.
	Guarded bool
}

// written is the key a slot of this layer is spelled as in the file.
func (l Layer) written(slot string) string {
	if l.As != "" {
		return l.As
	}
	return slot
}

// statusSlots are the colours that mean something: ok, needs attention, failed,
// and the text on the two badges that say the last two. They are the operator's
// to set, in their own named config file, and no guarded layer may state them.
var statusSlots = []string{"good", "warn", "bad", "inverse", "ink"}

// IsStatus reports whether slot is one of the status colours, the ones only the
// operator's own named config file can state. Asked by whatever offers a slot to
// be written — an editor, a flag, a completion — so that none of them offers a
// line the palette would always leave out.
func IsStatus(slot string) bool { return slices.Contains(statusSlots, slot) }

// textSlots are the colours text is drawn in, which a guarded layer may state
// as long as the text can still be seen.
var textSlots = []string{"primary", "label", "accent", "muted", "faint"}

// contrastFloor is the contrast ratio a guarded text colour keeps against pure
// black and against pure white.
//
// **A floor against being invisible on either kind of terminal, not a standard
// for legibility.** The built-in palette is chosen at 3.4:1 against white and
// 6.2:1 against black, which is a standard; an environment's colours are an
// outside party's choice on the operator's screen, and what that must not be
// able to do is paint the whole of it in a colour that is not there — #101010 is
// 1.1:1 on a black terminal. 3:1, the usual figure for large text, would refuse
// the built-in faint (2.4:1 on black), which is structure and is meant to recede.
// 1.5:1 refuses what cannot be seen and nothing a person would choose to see.
const contrastFloor = 1.5

// contrastOnBlack and contrastOnWhite are WCAG's contrast ratio of a colour
// against pure black and pure white, from the relative luminance Badge picks its
// ink by.
func contrastOnBlack(hex string) float64 { return (relativeLuminance(hex) + 0.05) / 0.05 }
func contrastOnWhite(hex string) float64 { return 1.05 / (relativeLuminance(hex) + 0.05) }

// distinct is a status the primary may not be, and what it would cost.
type distinct struct{ status, why string }

// primaryApart is what the primary may not be: the identity colour is where the
// cursor is, and a cursor drawn in the colour of a status reads as that status.
// Exact equality and no more — "close to" is a judgement about eyesight that no
// threshold here would get right for everybody, and a status colour a guarded
// layer cannot state is not one it can swap or nudge either.
var primaryApart = []distinct{
	{"good", "the selection would read as a healthy status"},
	{"warn", "the selection would read as a warning"},
	{"bad", "the selection would read as a failure"},
}

// Resolve folds layers, lowest first, into the overrides a palette is built
// from, and says what it could not use. The built-in palette sits beneath the
// first layer, so a slot no layer states is the built-in.
//
// **An environment may recolour the screen; it may not make one thing read as
// another.** The operator's own `theme:` is theirs, the status colours included:
// stating good and bad the same is a choice about their screen, made once, in
// the file they edit. An environment's palette is applied by a switch, and the
// screen is where a person learns what a refusal, a warning and a selection are
// — so a guarded layer (an environment's colour, an environment's theme, the
// `theme:` of a file nobody named) is held to three rules, each leaving the
// entry out, the layer beneath it standing, and a problem naming it:
//
//   - it may not state the status colours at all (statusSlots). Nearly equal,
//     swapped, or the badge's text a digit from its background are all ways of
//     making one thing read as another, and a threshold would have to guess what
//     is too near for everybody's eyes; a slot it cannot state needs no guess.
//   - a colour for text keeps contrastFloor against black and against white, so
//     that it is not invisible on one of the two kinds of terminal.
//   - the primary, whether stated or derived from a profile's colour, is not
//     exactly a status colour that stands. Left out, the colour beneath it is
//     judged in its turn, since leaving an entry out can bring a lower one into
//     the way.
//
// What collides without any guarded layer's help — the operator's own block, or
// the built-ins — is not Resolve's to fix.
func Resolve(layers ...Layer) (map[string]string, []Problem) {
	kept := make([]map[string]string, len(layers))
	var problems []Problem
	for i, l := range layers {
		colors := l.Colors
		if l.Guarded {
			var ps []Problem
			colors, ps = withoutStatus(l, colors)
			problems = append(problems, ps...)
		}
		var ps []Problem
		kept[i], ps = validate(l.Source, l.As, colors)
		problems = append(problems, ps...)
		if l.Guarded {
			kept[i], ps = withoutUnseen(l, kept[i])
			problems = append(problems, ps...)
		}
	}
	left := make([]map[string]bool, len(layers))
	for i := range left {
		left[i] = map[string]bool{}
	}
	for {
		palette, owner := fold(kept, left)
		i := owner["primary"]
		if i >= 0 && layers[i].Guarded {
			if apart, same := primaryOfAStatus(palette); same {
				left[i]["primary"] = true
				problems = append(problems, Problem{Source: layers[i].Source, Field: layers[i].written("primary"),
					Reason: fmt.Sprintf("is the same colour as %s, so %s", apart.status, apart.why),
					Hint:   "left out, so the colour beneath it stands — pick one the status colours do not share"})
				continue
			}
		}
		overrides := make(map[string]string, len(palette))
		for slot, i := range owner {
			if i >= 0 {
				overrides[slot] = palette[slot]
			}
		}
		return overrides, problems
	}
}

// primaryOfAStatus is the status the palette's primary is exactly, without
// regard to how either is spelled.
func primaryOfAStatus(palette map[string]string) (distinct, bool) {
	for _, apart := range primaryApart {
		if strings.EqualFold(palette["primary"], palette[apart.status]) {
			return apart, true
		}
	}
	return distinct{}, false
}

// withoutStatus is the colours a guarded layer states that are not a status
// colour, and a problem for each that is. Judged before the colour is, so that
// `good: red` is told it is a status and not that it is not a colour: fixing
// the second would only find the first.
func withoutStatus(l Layer, colors map[string]string) (map[string]string, []Problem) {
	var problems []Problem
	rest := make(map[string]string, len(colors))
	for _, slot := range sortedKeys(colors) {
		if !IsStatus(slot) {
			rest[slot] = colors[slot]
			continue
		}
		problems = append(problems, Problem{Source: l.Source, Field: l.written(slot),
			Reason: "is a status colour, which only your own config file sets",
			Hint: "left out, so the colour beneath it stands — set it under theme: in your own config file, " +
				"e.g. `rta config set theme." + slot + " '#3ED598'`"})
	}
	return rest, problems
}

// withoutUnseen is the colours a guarded layer states for text that keep
// contrastFloor against both black and white, and a problem for each that does
// not, naming the ratio it measured and the fix.
func withoutUnseen(l Layer, colors map[string]string) (map[string]string, []Problem) {
	var problems []Problem
	seen := make(map[string]string, len(colors))
	for _, slot := range sortedKeys(colors) {
		hex := colors[slot]
		if !slices.Contains(textSlots, slot) {
			seen[slot] = hex
			continue
		}
		ratio, against, terminal := contrastOnBlack(hex), "black", "dark"
		if white := contrastOnWhite(hex); white < ratio {
			ratio, against, terminal = white, "white", "light"
		}
		if ratio >= contrastFloor {
			seen[slot] = hex
			continue
		}
		// Truncated and not rounded, so a colour that fails by a hair never
		// reads as the floor itself.
		shown := math.Floor(ratio*100) / 100
		p := Problem{Source: l.Source, Field: l.written(slot),
			Reason: fmt.Sprintf("is %.2f:1 against %s, so text in it would all but vanish on a %s terminal", shown, against, terminal),
			Hint: fmt.Sprintf("left out, so the colour beneath it stands — pick one that keeps at least %.1f:1 "+
				"against both black and white", contrastFloor)}
		if l.As != "" {
			p.Reason = fmt.Sprintf("is %.2f:1 against %s, so as the screen's primary it would all but vanish on a %s terminal",
				shown, against, terminal)
			p.Hint = fmt.Sprintf("the badge keeps it, and the screen's primary stays as it was beneath — pick one that "+
				"keeps at least %.1f:1 against both black and white for the screen to follow it", contrastFloor)
		}
		problems = append(problems, p)
	}
	return seen, problems
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// fold is the palette the layers make, every slot, and which layer each slot's
// colour came from (-1 for the built-in) — entries named in left do not count.
func fold(kept []map[string]string, left []map[string]bool) (palette map[string]string, owner map[string]int) {
	palette = make(map[string]string, len(fields))
	owner = make(map[string]int, len(fields))
	for slot, e := range fields {
		palette[slot], owner[slot] = e.builtin, -1
	}
	for i, colors := range kept {
		for slot, hex := range colors {
			if !left[i][slot] {
				palette[slot], owner[slot] = hex, i
			}
		}
	}
	return palette, owner
}

// ApplyLayers is Apply for a palette made of layers: Resolve, then the result.
func ApplyLayers(layers ...Layer) []Problem {
	overrides, problems := Resolve(layers...)
	Apply(overrides)
	return problems
}

// Effective is every slot's colour once overrides are applied over the
// built-ins, without touching the live palette: what Current will answer after
// Apply, for a caller that has to know whether applying would change anything,
// or what a form would show.
func Effective(overrides map[string]string) map[string]string {
	usable, _ := validate("", "", overrides)
	out := make(map[string]string, len(fields))
	for slot, e := range fields {
		out[slot] = e.builtin
	}
	for slot, hex := range usable {
		out[slot] = hex
	}
	return out
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
	case v == "" || isName(v):
		return StatusNeutral
	case isWord(v, "ok") || strings.HasPrefix(v, "valid") ||
		isWord(v, "open") || isWord(v, "up") ||
		strings.HasPrefix(v, "active") || isWord(v, "read") || isWord(v, "ready"):
		return StatusGood
	case strings.HasPrefix(v, "warn") || strings.HasPrefix(v, "write") ||
		strings.HasPrefix(v, "pending") || strings.HasPrefix(v, "busy") ||
		// pkg's one word for a package behind its latest. Amber, not red:
		// nothing is broken, an upgrade is available, and the table is the
		// place somebody looks to decide whether to take it.
		strings.HasPrefix(v, "outdated") ||
		// A plugin found on $PATH and not run. Amber rather than red: nothing
		// is wrong, a decision is outstanding — and rather than neutral,
		// because "installed and doing nothing" is the state a trust gate has
		// to make impossible to miss.
		strings.HasPrefix(v, "untrusted") ||
		// A part of an overview its credential cannot read (the nodes of a
		// namespaced token), said beside the parts it could: the answer is
		// partial and the page still worth reading, so amber and not red.
		strings.HasPrefix(v, "could not be read") ||
		// An overview with no reading of its backend, or of a backend that is up
		// and has not been set up: docker and qdrant already lead with the first,
		// and a Vault that was never initialised is the second.
		strings.HasPrefix(v, "unreadable") || strings.HasPrefix(v, "not initialized"):
		return StatusWarn
	case strings.HasPrefix(v, "error") || strings.HasPrefix(v, "expired") ||
		strings.HasPrefix(v, "invalid") || strings.HasPrefix(v, "fail") ||
		strings.HasPrefix(v, "denied") || strings.HasPrefix(v, "unreachable") ||
		strings.HasPrefix(v, "destructive") || isWord(v, "down") ||
		strings.HasPrefix(v, "overloaded") || strings.HasPrefix(v, "eol") ||
		strings.HasPrefix(v, "critical") ||
		// A note past its due date, the one grade the note list gives that a
		// person is meant to act on today, and it was the one drawn plainest.
		strings.HasPrefix(v, "overdue") ||
		// How an overview says its backend is not there, in the one line that is
		// its headline: the docker daemon and the kube cluster answer with a
		// key/value page rather than an error, so that the page can still say
		// what the configuration knows. They were drawn plainest, on a dashboard
		// of green ones.
		strings.HasPrefix(v, "did not answer") || strings.HasPrefix(v, "was never contacted") ||
		strings.HasPrefix(v, "answered, and refused") || strings.HasPrefix(v, "answered, and did not accept") ||
		// A store that is up and answers nothing until somebody opens it: Vault's
		// seal state, the one thing its overview exists to report.
		strings.HasPrefix(v, "sealed"):
		return StatusBad
	case strings.HasPrefix(v, "closed") || isWord(v, "info") ||
		isWord(v, "none") || strings.HasPrefix(v, "disabled") ||
		isWord(v, "done"):
		return StatusMuted
	default:
		return StatusNeutral
	}
}

// isName is whether the text leads with a name rather than a word of the
// vocabulary: a first token with a hyphen, underscore, dot or slash in it is an
// identifier — `open-webui-pg-cluster-2`, `upstream.example.com`, `down/a` — and
// the words above are stems, so reading it by its first letters graded a
// cluster called open-webui green on the page that names it. The token ends at
// a space or at the punctuation a status line puts after its word, so
// "ERROR: connection refused" and "ok — 3 pods" still lead with their word.
func isName(v string) bool {
	end := strings.IndexAny(v, " \t,;:()—·")
	if end < 0 {
		end = len(v)
	}
	return strings.ContainsAny(v[:end], "-_./")
}

// isWord is whether v leads with word as a whole word, for the short stems that
// are also the first letters of names and of ordinary words: "up" is a state and
// "upstream" is not, "ok" is one and "okta" is not, "down" and "downloads".
// The longer stems (warn, fail, error, expire…) stay prefixes, because their
// inflections — warning, failed, errors — are the words the plugins emit.
func isWord(v, word string) bool {
	rest, ok := strings.CutPrefix(v, word)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	c := rest[0]
	return (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_'
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
	// NaN is a number as far as ParseFloat is concerned — it reads "NaN",
	// "Inf" and "Infinity" case-insensitively and returns a nil error — and
	// every comparison against NaN is false, so it would fall past both bands
	// to the comfortable one and paint "could not measure this" green. That is
	// the precise opposite of what the paragraph above promises, and it is the
	// one wrong answer that matters: a cell rta cannot read must never claim
	// there is room. Reachable from any plugin that declares KindUsage, since
	// the cell is whatever text it wrote.
	if err != nil || math.IsNaN(pct) || math.IsInf(pct, 0) {
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

// Mark is how the environment in force is named on a screen: the badge in its
// own colour when it has one, the bullet an environment nobody marked keeps, and
// where there is no colour to draw either in, the name between brackets.
//
// **The brackets are the mark when there is no colour to be one**, for a marked
// environment and an unmarked one alike: a badge is a filled block, and with the
// fill taken away it is a bare word, indistinguishable from the text it sits in
// and less marked than a bullet. Drawn here and not by each surface because
// there are two — the line before a command and the header of the TUI — and
// they had decided it twice, differently. ascii is a terminal that cannot show
// the bullet.
func Mark(name, hex string, noColor, ascii bool) string {
	switch {
	case noColor:
		return "[ " + name + " ]"
	case HexColor.MatchString(hex):
		return Badge(name, hex)
	case ascii:
		return GoodText.Render("* " + name)
	}
	return GoodText.Render("● " + name)
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
