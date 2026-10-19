package theme

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// **The palette is judged by how far apart its colours are, not by how they
// look to whoever last edited it.** The clay orange this guard was written
// against looked fine on the screen of everyone who chose it, and was 14 from
// the red of a failure for a person with normal vision and 0.9 from it for a
// deuteranope: a selected tile and a failed one drew the same border. Nothing
// else in the suite could have caught that, because nothing else measures.
//
// Distance is CIEDE2000 (ΔE below), the CIE's own model of how far apart two
// colours look, and the colour-blind eye is Machado, Oliveira and Fernandes's
// 2009 model at full severity, applied in linear light and rounded back to the
// eight bits a terminal draws. The maths is tested against published values
// below, so a figure in this file is not an artefact of a slip in it.
//
// Each threshold is a floor, and a floor is not what the palette holds: the
// comment on each says what it holds today, in which kind of eye (normal vision,
// or a protanope's or a deuteranope's, which are the colour-blind figures), and
// why the floor is where it is. Where a pair cannot reach the floor a pair of
// that kind ought to have, the floor is set at what it does hold and says so; a
// colour change that fails one of them is a decision to take in the open, in
// this file, and not by editing a hex.

const (
	// atAGlance is the ΔE at which two colours read as two colours and not as
	// two shades of one. One unit is the least a trained eye separates side by
	// side and two or three is a shade; twenty is a different colour at a glance,
	// which is how a border, a word or a marker is read on a screen.
	atAGlance = 20.0
	// farFromAStatus holds the primary away from red, amber and green for every
	// kind of eye. It holds 31.6 in normal vision (from bad), 40 as a protanope
	// sees them (bad again) and 35.6 as a deuteranope does (from good); the orange
	// it replaced was 14 from bad, and 0.9 for a deuteranope. The primary is where
	// the cursor is, and a cursor that can pass for a status is read as that status.
	farFromAStatus = 30.0
	// accentFromWarn and accentFromWarnColourBlind are the limits the accent was
	// searched against, and they sit well under what it holds (55 in normal
	// vision, 58 and 62 colour-blind). They are over twice and ten times the 11 and
	// 1 the amber it replaced gave; a match of a filter and a warning are both a
	// small coloured thing in a table.
	accentFromWarn            = 25.0
	accentFromWarnColourBlind = 10.0
	// labelFromPrimaryColourBlind is how far the pane name stays from the primary
	// for a protanope or a deuteranope, where it holds 14.9 and 12.7. In normal
	// vision it holds 21.7 against a floor of atAGlance: a title is not a key.
	labelFromPrimaryColourBlind = 12.0
	// labelFromStatus keeps a pane name from passing for a status in normal vision,
	// since tile bodies are full of them; it holds 27.2 from good, the nearest.
	// Colour-blind the pairs are held to atAGlance and not to this, because the
	// weakest of them is 24.8 (from bad, for a protanope) and 25.0 (from good, for
	// a deuteranope): a floor of 25 would refuse the palette.
	labelFromStatus = 25.0
	// accentFromPrimaryColourBlind is held at what the pair gives, and says so. In
	// normal vision the pair is 29.2 apart and held to atAGlance. A violet reads as
	// a blue to a protanope or a deuteranope, so there it holds 4.5 and 8.7, and
	// the most any colour meeting the accent's other limits could give is 7.5 and
	// 10: every hue that is 25 from warn and 20 from bad, good and the primary is
	// a blue. The pair is a prompt or a cursor glyph beside text and the primary a
	// key or a title, told by where they are. The floor still catches the accent
	// becoming the primary.
	accentFromPrimaryColourBlind = 4.0
	// accentFromLabel is held at what the pair gives: 15.2 in normal vision, 18.0
	// and 17.4 colour-blind. Both are cool, so it is the nearest pair in the
	// palette whose members mean different things, and 15 is as far as the accent
	// can go from the label at the status colours' luminance.
	accentFromLabel = 15.0
	// labelFromMuted and labelFromMutedColourBlind are held at what the pair
	// gives, which is less than the slate blue label did: 15.9 in normal vision
	// (11.8 before), but 4.5 for a protanope and 8.3 for a deuteranope (9.2 and
	// 10.0 before). The label is the quiet colour and muted text is the quietest,
	// and the corner the label's other limits leave has nothing better than 5.2
	// for a protanope. A pane's name is bold and in the border, and muted text is
	// the body, told apart by where it is; the floor stops it getting worse.
	labelFromMuted            = 15.0
	labelFromMutedColourBlind = 4.0
	// statusesApart and statusesApartColourBlind are the floors for the three
	// status colours among themselves, and they are not what a pair of statuses
	// should have: warn and bad are 35.8 apart in normal vision but 7.5 apart
	// under deuteranopia. Those colours are not this guard's to change, and a
	// status is never carried by its colour alone — the word in the cell (ok,
	// warn, fail) is the grade, and on a terminal with no colour it is all there
	// is — so the floors are what holds today, and a status colour that moves
	// closer to another fails here.
	statusesApart            = 30.0
	statusesApartColourBlind = 7.0

	// accentChromaFloor is the CIELAB chroma below which the accent stops reading
	// as a colour beside body text and starts reading as a grey. Azure is 66.
	accentChromaFloor = 40.0
	// labelChromaShare is how much of the primary's chroma the label may have at
	// most: a pane name is subordinate to the content it names, so it is the
	// quiet colour. Steel teal has 28% of it.
	labelChromaShare = 0.5

	// onDarkFloor and onWhiteFloor are the WCAG contrast a text colour keeps on
	// the dark terminal the palette is drawn for and on a white one. 4.5 is the
	// standard for body text; 3.0 is the standard for bold and large text, which
	// is what a pane name and a key are, and a light terminal is the rarer one.
	// Faint is structure and meant to recede, so it keeps contrastFloor, the line
	// below which it cannot be seen.
	onDarkFloor  = 4.5
	onWhiteFloor = 3.0

	// darkReference is the background the palette is judged on. It is a literal
	// and not inkHex, so that retuning the badge ink does not move the ground the
	// colours are measured against.
	darkReference  = "#1A1A22"
	whiteReference = "#FFFFFF"
)

// guardedSlots are the palette's colours as the built-ins write them down, the
// same strings Apply resets to.
func guardedSlots() map[string]string {
	return map[string]string{
		"primary": primaryHex, "accent": accentHex, "label": labelHex,
		"muted": mutedHex, "faint": faintHex,
		"good": goodHex, "warn": warnHex, "bad": badHex,
	}
}

// guardPair is two slots and the least ΔE they keep in normal vision, under
// protanopia and under deuteranopia, in the order of guardVisions.
type guardPair struct {
	a, b string
	min  [3]float64
}

func everywhere(d float64) [3]float64 { return [3]float64{d, d, d} }

var guardPairs = []guardPair{
	{"primary", "warn", everywhere(farFromAStatus)},
	{"primary", "bad", everywhere(farFromAStatus)},
	{"primary", "good", everywhere(farFromAStatus)},
	{"primary", "accent", [3]float64{atAGlance, accentFromPrimaryColourBlind, accentFromPrimaryColourBlind}},
	{"primary", "label", [3]float64{atAGlance, labelFromPrimaryColourBlind, labelFromPrimaryColourBlind}},
	{"accent", "warn", [3]float64{accentFromWarn, accentFromWarnColourBlind, accentFromWarnColourBlind}},
	{"accent", "bad", everywhere(atAGlance)},
	{"accent", "good", everywhere(atAGlance)},
	{"accent", "label", everywhere(accentFromLabel)},
	{"label", "muted", [3]float64{labelFromMuted, labelFromMutedColourBlind, labelFromMutedColourBlind}},
	{"label", "warn", [3]float64{labelFromStatus, atAGlance, atAGlance}},
	{"label", "bad", [3]float64{labelFromStatus, atAGlance, atAGlance}},
	{"label", "good", [3]float64{labelFromStatus, atAGlance, atAGlance}},
	{"warn", "bad", [3]float64{statusesApart, statusesApartColourBlind, statusesApartColourBlind}},
	{"warn", "good", [3]float64{statusesApart, statusesApartColourBlind, statusesApartColourBlind}},
	{"bad", "good", [3]float64{statusesApart, statusesApartColourBlind, statusesApartColourBlind}},
}

// guardReadable are the slots drawn as text on the background, held to the
// contrast floors on both references. Muted is secondary text and held to them
// too; faint is not text and is held to contrastFloor.
var guardReadable = []string{"primary", "accent", "label", "muted", "good", "warn", "bad"}

// paletteProblems is every threshold in this file the palette fails, one line
// each, and nothing when it holds. Taking the palette as an argument is what lets
// the guard be shown to fail on the one it replaced.
func paletteProblems(palette map[string]string) []string {
	var problems []string
	for _, p := range guardPairs {
		for i, v := range guardVisions {
			got := deltaE(seenAs(palette[p.a], v), seenAs(palette[p.b], v))
			if got < p.min[i] {
				problems = append(problems, fmt.Sprintf("%s %s vs %s %s is %.1f ΔE under %s, under the %.0f it keeps",
					p.a, palette[p.a], p.b, palette[p.b], got, v.name, p.min[i]))
			}
		}
	}
	for _, slot := range guardReadable {
		hex := palette[slot]
		if got := contrast(hex, darkReference); got < onDarkFloor {
			problems = append(problems, fmt.Sprintf("%s %s is %.2f:1 on %s, under %.1f", slot, hex, got, darkReference, onDarkFloor))
		}
		if got := contrast(hex, whiteReference); got < onWhiteFloor {
			problems = append(problems, fmt.Sprintf("%s %s is %.2f:1 on white, under %.1f", slot, hex, got, onWhiteFloor))
		}
	}
	for _, ref := range []string{darkReference, whiteReference} {
		if got := contrast(palette["faint"], ref); got < contrastFloor {
			problems = append(problems, fmt.Sprintf("faint %s is %.2f:1 on %s, under %.1f", palette["faint"], got, ref, contrastFloor))
		}
	}
	if c := chroma(palette["accent"]); c < accentChromaFloor {
		problems = append(problems, fmt.Sprintf("accent %s has a chroma of %.0f, under %.0f: it would read as grey", palette["accent"], c, accentChromaFloor))
	}
	if c, p := chroma(palette["label"]), chroma(palette["primary"]); c > labelChromaShare*p {
		problems = append(problems, fmt.Sprintf("label %s has a chroma of %.0f, over %.0f%% of the primary's %.0f: it would not be the quiet one",
			palette["label"], c, labelChromaShare*100, p))
	}
	return problems
}

func TestThePaletteKeepsItsColoursApartForEveryKindOfEye(t *testing.T) {
	for _, problem := range paletteProblems(guardedSlots()) {
		t.Error(problem)
	}
}

// The built-in colours this guard retired: the clay orange primary, the amber
// accent and the slate blue label. They are literals and not read from the
// palette, since what makes them what they are is that they are the old ones,
// whatever the built-ins are retuned to next. violetPrimary is the primary that
// replaced the orange, kept the same way: the slate blue was found to clash with
// it, and the test below needs that pair and not the primary of the day.
const (
	retiredPrimary = "#D97757"
	retiredAccent  = "#D77015"
	retiredLabel   = "#7794B6"
	violetPrimary  = "#BA73DE"
)

// refusal is everything the guard says about a palette, and pairLine the start of
// the line it writes for one pair, so a test names the pair it expects refused.
func refusal(palette map[string]string) string {
	return strings.Join(paletteProblems(palette), "\n")
}

func pairLine(palette map[string]string, a, b string) string {
	return fmt.Sprintf("%s %s vs %s %s", a, palette[a], b, palette[b])
}

// The guard has to fail on the palette it replaced, or its thresholds are
// numbers nobody ever checked against the thing they were written for. Each of
// the retired colours that clashed with a status is put back alone, so it is
// known that the guard needs no help from the other slots to refuse it, and the
// pairs expected are ones only the retired colour and a status decide: retuning
// the built-in primary, accent or label cannot make this pass or fail.
func TestTheGuardRefusesThePaletteItReplaced(t *testing.T) {
	for _, c := range []struct{ slot, retired, against string }{
		{"primary", retiredPrimary, "bad"},
		{"primary", retiredPrimary, "warn"},
		{"accent", retiredAccent, "warn"},
	} {
		palette := guardedSlots()
		palette[c.slot] = c.retired
		if want := pairLine(palette, c.slot, c.against); !strings.Contains(refusal(palette), want) {
			t.Errorf("%s put back to %s was not refused as %q; the guard said:\n%s", c.slot, c.retired, want, refusal(palette))
		}
	}

	old := guardedSlots()
	old["primary"], old["accent"], old["label"] = retiredPrimary, retiredAccent, retiredLabel
	if got := paletteProblems(old); len(got) < 3 {
		t.Errorf("the whole retired palette failed %d checks, want at least the three above:\n%s", len(got), strings.Join(got, "\n"))
	}
}

// The slate blue was fine beside the orange, so it is not refused with the rest
// of the retired palette: it was refused beside the violet, 20.2 ΔE from it in
// normal vision, which clears the floor, and 11.1 and 7.6 as a protanope and a
// deuteranope see them, under the 12 the label keeps. That pair is built from the
// two literals above and not from the palette, so it does not move when the
// built-ins do.
func TestTheGuardRefusesTheSlateBlueLabelBesideTheViolet(t *testing.T) {
	palette := guardedSlots()
	palette["primary"], palette["label"] = violetPrimary, retiredLabel
	if want := pairLine(palette, "primary", "label"); !strings.Contains(refusal(palette), want) {
		t.Errorf("the slate blue beside the violet was not refused as %q; the guard said:\n%s", want, refusal(palette))
	}
}

// The other side of the same test: a palette that breaks a floor is refused for
// that floor, whatever the built-ins are. Each case takes the palette of the day
// and puts into one slot a colour that cannot clear the floor named (a status, the
// primary or the ground itself), so no case depends on a particular default.
func TestTheGuardRefusesAPaletteThatBreaksAFloor(t *testing.T) {
	current := guardedSlots()
	for _, c := range []struct{ slot, hex, a, b string }{
		{"primary", current["warn"], "primary", "warn"},
		{"primary", current["bad"], "primary", "bad"},
		{"accent", current["primary"], "primary", "accent"},
		{"accent", current["good"], "accent", "good"},
		{"label", current["primary"], "primary", "label"},
		{"label", current["accent"], "accent", "label"},
		{"label", current["muted"], "label", "muted"},
		{"label", current["bad"], "label", "bad"},
		{"bad", current["warn"], "warn", "bad"},
	} {
		palette := guardedSlots()
		palette[c.slot] = c.hex
		if want := pairLine(palette, c.a, c.b); !strings.Contains(refusal(palette), want) {
			t.Errorf("%s set to %s was not refused as %q; the guard said:\n%s", c.slot, c.hex, want, refusal(palette))
		}
	}
	for _, c := range []struct{ slot, hex, want string }{
		{"muted", darkReference, "muted " + darkReference + " is"},
		{"accent", whiteReference, "accent " + whiteReference + " is"},
		{"accent", "#808080", "accent #808080 has a chroma"},
		{"label", current["primary"], "label " + current["primary"] + " has a chroma"},
		{"faint", darkReference, "faint " + darkReference + " is"},
	} {
		palette := guardedSlots()
		palette[c.slot] = c.hex
		if !strings.Contains(refusal(palette), c.want) {
			t.Errorf("%s set to %s was not refused as %q; the guard said:\n%s", c.slot, c.hex, c.want, refusal(palette))
		}
	}
}

// The maths is checked against the values the CIE's authors published for it
// (Sharma, Wu and Dalal, 2005, the test data for CIEDE2000), against the one
// contrast ratio everybody knows, and against the property a colour-vision model
// must have: a grey is grey to every eye.
func TestTheColourMathsMatchesPublishedValues(t *testing.T) {
	for _, c := range []struct {
		a, b lab
		want float64
	}{
		{lab{50, 2.6772, -79.7751}, lab{50, 0, -82.7485}, 2.0425},
		{lab{50, 3.1571, -77.2803}, lab{50, 0, -82.7485}, 2.8615},
		{lab{50, 2.5, 0}, lab{73, 25, -18}, 27.1492},
		{lab{50, 2.5, 0}, lab{61, -5, 29}, 22.8977},
		{lab{50, 2.5, 0}, lab{56, -27, -3}, 31.9030},
		{lab{50, 2.5, 0}, lab{58, 24, 15}, 19.4535},
		{lab{50, 2.5, 0}, lab{50, 0, -2.5}, 4.3065},
		{lab{50, 2.5, 0}, lab{50, 3.2972, 0}, 1.0000},
	} {
		if got := deltaE(c.a, c.b); math.Abs(got-c.want) > 0.0001 {
			t.Errorf("ΔE(%v, %v) = %.4f, want %.4f", c.a, c.b, got, c.want)
		}
	}
	if got := contrast("#000000", "#FFFFFF"); math.Abs(got-21) > 1e-9 {
		t.Errorf("black on white is %.4f:1, want 21:1", got)
	}
	for _, v := range guardVisions {
		if got := deltaE(seenAs("#808080", v), seenAs("#808080", guardVisions[0])); got > 0.5 {
			t.Errorf("a mid grey moves %.2f ΔE under %s, want it unchanged", got, v.name)
		}
	}
	// sRGB to CIELAB under D65 for the three primaries, to the two decimals the
	// standard's tables and an independent implementation (go-colorful) agree on.
	for _, c := range []struct {
		hex  string
		want lab
	}{
		{"#FF0000", lab{53.24, 80.09, 67.20}},
		{"#00FF00", lab{87.73, -86.18, 83.18}},
		{"#0000FF", lab{32.30, 79.19, -107.86}},
	} {
		got := seenAs(c.hex, guardVisions[0])
		if math.Abs(got.l-c.want.l) > 0.02 || math.Abs(got.a-c.want.a) > 0.02 || math.Abs(got.b-c.want.b) > 0.02 {
			t.Errorf("%s reads as Lab %.2f %.2f %.2f, want %.2f %.2f %.2f", c.hex, got.l, got.a, got.b, c.want.l, c.want.a, c.want.b)
		}
	}
}

// publishedMatrices are typed in a second time, from the authors' own tables:
// Machado, Oliveira and Fernandes, "A Physiologically-based Model for Simulation
// of Color Vision Deficiency", IEEE Transactions on Visualization and Computer
// Graphics 15(6), 2009, whose page (inf.ufrgs.br/~oliveira/pubs_files/CVD_Simulation)
// prints the matrices for every severity. Severity 1.0 is the complete absence of
// the cone: the protanomaly column is protanopia and the deuteranomaly column
// deuteranopia. A second copy is the point of it. A digit slipped in guardVisions
// that leaves each row summing to 1 moves every figure in this file, and nothing
// else would notice.
var publishedMatrices = map[string][3][3]float64{
	"protanopia": {
		{0.152286, 1.052583, -0.204868},
		{0.114503, 0.786281, 0.099216},
		{-0.003882, -0.048116, 1.051998},
	},
	"deuteranopia": {
		{0.367322, 0.860646, -0.227968},
		{0.280085, 0.672501, 0.047413},
		{-0.011820, 0.042940, 0.968881},
	},
}

func TestTheColourBlindnessMatricesAreTheOnesTheAuthorsPublished(t *testing.T) {
	for _, v := range guardVisions[1:] {
		want, published := publishedMatrices[v.name]
		if !published {
			t.Fatalf("no published matrix for %s", v.name)
		}
		if *v.matrix != want {
			t.Errorf("the %s matrix is %v, the published one is %v", v.name, *v.matrix, want)
		}
		// A grey stays grey only if every row sums to 1, which the published rows do
		// to the six decimals they are printed in.
		for i, row := range *v.matrix {
			if sum := row[0] + row[1] + row[2]; math.Abs(sum-1) > 2e-6 {
				t.Errorf("row %d of the %s matrix sums to %.6f, want 1", i, v.name, sum)
			}
		}
	}
	if len(publishedMatrices) != len(guardVisions)-1 {
		t.Errorf("%d published matrices for %d colour-blind eyes", len(publishedMatrices), len(guardVisions)-1)
	}
}

// The matrix is right and so is the arithmetic that applies it, shown on colours
// worked out apart from this file. A pure primary is a column of the matrix, so
// the first three can be checked by reading the table above: red to a protanope
// is (0.152286, 0.114503, -0.003882) in linear light, the last clamped to 0, and
// the sRGB transfer function (IEC 61966-2-1) turns 0.152286 into 109, which is 6D,
// and 0.114503 into 95, which is 5F, so #6D5F00. Green is (1.052583, 0.786281,
// -0.048116), clamped to (1, 0.786281, 0) and drawn as #FFE500. The rest, the
// three status colours among them, come from two other implementations of the
// same published matrices that were run for this and agreed on every one: a
// short script, and go-colorful's own linear-light conversions. The inputs are
// literals, so retuning the palette cannot move them.
func TestTheColourBlindEyeReceivesTheColoursAnIndependentComputationGives(t *testing.T) {
	for _, c := range []struct{ hex, protanopia, deuteranopia string }{
		{"#FF0000", "#6D5F00", "#A39000"},
		{"#00FF00", "#FFE500", "#EFD63A"},
		{"#0000FF", "#0059FF", "#003DFB"},
		{"#BF7E00", "#958200", "#A49209"},
		{"#FF4357", "#797256", "#A89A51"},
		{"#229F6C", "#9D9269", "#8E886F"},
		{"#FFFFFF", "#FFFFFF", "#FFFFFF"},
	} {
		want := map[string]string{"normal vision": c.hex, "protanopia": c.protanopia, "deuteranopia": c.deuteranopia}
		for _, v := range guardVisions {
			if got := simulatedHex(c.hex, v); got != want[v.name] {
				t.Errorf("%s as %s is %s, want %s", c.hex, v.name, got, want[v.name])
			}
		}
	}
}

type lab struct{ l, a, b float64 }

// guardVision is one way of seeing: the matrix that maps linear sRGB to what that
// eye receives, nil for normal vision. Machado, Oliveira and Fernandes (2009),
// severity 1.0, the complete absence of the cone.
type guardVision struct {
	name   string
	matrix *[3][3]float64
}

var guardVisions = []guardVision{
	{"normal vision", nil},
	{"protanopia", &[3][3]float64{
		{0.152286, 1.052583, -0.204868},
		{0.114503, 0.786281, 0.099216},
		{-0.003882, -0.048116, 1.051998},
	}},
	{"deuteranopia", &[3][3]float64{
		{0.367322, 0.860646, -0.227968},
		{0.280085, 0.672501, 0.047413},
		{-0.011820, 0.042940, 0.968881},
	}},
}

func linearToSRGB(c float64) float64 {
	c = math.Max(0, math.Min(1, c))
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// simulatedHex is the eight-bit colour an eye of the given kind receives for hex:
// its linear light put through the eye's matrix, clamped to what a screen can
// draw, and written as the colour a terminal would draw. Normal vision receives
// it as it is.
func simulatedHex(hex string, v guardVision) string {
	if v.matrix == nil {
		return strings.ToUpper(hex)
	}
	rgb := [3]float64{channel(hex[1:3]), channel(hex[3:5]), channel(hex[5:7])}
	var drawn [3]int
	for i := range drawn {
		var seen float64
		for j := range rgb {
			seen += v.matrix[i][j] * rgb[j]
		}
		drawn[i] = int(math.Round(linearToSRGB(seen) * 255))
	}
	return fmt.Sprintf("#%02X%02X%02X", drawn[0], drawn[1], drawn[2])
}

// seenAs is the colour as an eye of the given kind receives it, in CIELAB: what
// simulatedHex draws, read as Lab under D65.
func seenAs(hex string, v guardVision) lab {
	drawn := simulatedHex(hex, v)
	rgb := [3]float64{channel(drawn[1:3]), channel(drawn[3:5]), channel(drawn[5:7])}
	x := (0.4124564*rgb[0] + 0.3575761*rgb[1] + 0.1804375*rgb[2]) / 0.95047
	y := 0.2126729*rgb[0] + 0.7151522*rgb[1] + 0.0721750*rgb[2]
	z := (0.0193339*rgb[0] + 0.1191920*rgb[1] + 0.9503041*rgb[2]) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return lab{116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)}
}

func chroma(hex string) float64 {
	c := seenAs(hex, guardVisions[0])
	return math.Hypot(c.a, c.b)
}

// contrast is WCAG's ratio between two colours, the lighter over the darker.
func contrast(a, b string) float64 {
	hi, lo := relativeLuminance(a), relativeLuminance(b)
	if lo > hi {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// deltaE is CIEDE2000, as Sharma, Wu and Dalal write it down.
func deltaE(c1, c2 lab) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	deg := func(r float64) float64 { return r * 180 / math.Pi }
	pow7 := func(x float64) float64 { return math.Pow(x, 7) }
	sq := func(x float64) float64 { return x * x }

	cBar := (math.Hypot(c1.a, c1.b) + math.Hypot(c2.a, c2.b)) / 2
	g := 0.5 * (1 - math.Sqrt(pow7(cBar)/(pow7(cBar)+pow7(25))))
	a1, a2 := (1+g)*c1.a, (1+g)*c2.a
	cp1, cp2 := math.Hypot(a1, c1.b), math.Hypot(a2, c2.b)
	hue := func(b, a float64) float64 {
		h := deg(math.Atan2(b, a))
		if h < 0 {
			h += 360
		}
		return h
	}
	hp1, hp2 := hue(c1.b, a1), hue(c2.b, a2)

	dL, dC := c2.l-c1.l, cp2-cp1
	var dh float64
	if cp1*cp2 != 0 {
		dh = hp2 - hp1
		if dh > 180 {
			dh -= 360
		} else if dh < -180 {
			dh += 360
		}
	}
	dH := 2 * math.Sqrt(cp1*cp2) * math.Sin(rad(dh/2))

	lBar, cpBar := (c1.l+c2.l)/2, (cp1+cp2)/2
	var hBar float64
	switch {
	case cp1*cp2 == 0:
		hBar = hp1 + hp2
	case math.Abs(hp1-hp2) <= 180:
		hBar = (hp1 + hp2) / 2
	case hp1+hp2 < 360:
		hBar = (hp1 + hp2 + 360) / 2
	default:
		hBar = (hp1 + hp2 - 360) / 2
	}
	t := 1 - 0.17*math.Cos(rad(hBar-30)) + 0.24*math.Cos(rad(2*hBar)) +
		0.32*math.Cos(rad(3*hBar+6)) - 0.20*math.Cos(rad(4*hBar-63))
	dTheta := 30 * math.Exp(-sq((hBar-275)/25))
	rc := 2 * math.Sqrt(pow7(cpBar)/(pow7(cpBar)+pow7(25)))
	sl := 1 + 0.015*sq(lBar-50)/math.Sqrt(20+sq(lBar-50))
	sc := 1 + 0.045*cpBar
	sh := 1 + 0.015*cpBar*t
	rt := -math.Sin(rad(2*dTheta)) * rc

	return math.Sqrt(sq(dL/sl) + sq(dC/sc) + sq(dH/sh) + rt*(dC/sc)*(dH/sh))
}
