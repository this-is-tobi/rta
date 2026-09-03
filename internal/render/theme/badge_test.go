package theme

import (
	"strings"
	"testing"
)

// **The ink is the only thing about a badge that can be wrong in a way nobody
// notices until it matters.** The colour is the operator's and rta has no
// opinion about it; what rta owes them is text they can read on it. White on
// the palette's own amber is unreadable and dark on its own ink is invisible,
// and either lands on the one label whose entire job is to be impossible to
// miss.
//
// Pinned as the decision rather than as the number: the table says which ink a
// colour earns, so a change to the threshold has to be argued for against
// these cases rather than merely compile.
func TestABadgeChoosesInkThatCanBeReadOnIt(t *testing.T) {
	for _, c := range []struct {
		hex  string
		dark bool // dark ink, i.e. the background is light
		why  string
	}{
		{"#FFFFFF", true, "white"},
		{"#000000", false, "black"},
		{warnHex, true, "the palette's amber — the case white ink fails on"},
		{inkHex, false, "the palette's own ink — the case dark ink disappears into"},
		{goodHex, true, "a mid green: green carries most of the luminance"},
		{primaryHex, true, "clay orange at luminance 0.286, the figure Label was calibrated against"},
		{badHex, true, "the palette's red, which is light enough for dark ink"},
	} {
		if got := relativeLuminance(c.hex) > 0.179; got != c.dark {
			t.Errorf("%s (%s): dark ink = %v, want %v (luminance %.3f)",
				c.hex, c.why, got, c.dark, relativeLuminance(c.hex))
		}
	}
}

// A profile with no colour is the ordinary case, not an error. A badge painted
// in some default would say "this environment is marked" about one that is not.
func TestSomethingThatIsNotAColourIsNotPainted(t *testing.T) {
	for _, hex := range []string{"", "red", "#FFF", "#GGGGGG", "FF6B7A", "#FF6B7A "} {
		if got := Badge("shop-prod", hex); got != "shop-prod" {
			t.Errorf("Badge(%q) = %q, want the text unchanged", hex, got)
		}
	}
	painted := Badge("shop-prod", "#FF6B7A")
	if !strings.Contains(painted, "shop-prod") {
		t.Errorf("the badge lost its text: %q", painted)
	}
	if painted == "shop-prod" {
		t.Error("a real colour painted nothing")
	}
}
