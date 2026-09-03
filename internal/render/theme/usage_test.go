package theme

import (
	"strconv"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A KindUsage cell is graded from the text a producer wrote, because that is
// all a view carries — the number and the capacity behind it do not survive
// the crossing. So the parser has to handle every spelling the producers use,
// and each of these is a real one somewhere in the tree.
func TestEveryPercentageSpellingTheProducersUseIsGraded(t *testing.T) {
	cases := map[string]StatusKind{
		// kube's percentOf: an integer and a sign.
		"12%":  StatusGood,
		"88%":  StatusWarn,
		"97%":  StatusBad,
		"100%": StatusBad,
		// stats.go's pct and fs's share: one decimal place.
		"0.0%":   StatusGood,
		"79.9%":  StatusGood,
		"80.0%":  StatusWarn,
		"89.9%":  StatusWarn,
		"90.0%":  StatusBad,
		"100.0%": StatusBad,
		// sys disk: no decimal place, and padding a renderer may have added.
		"  45%  ": StatusGood,
		"93 %":    StatusBad,
	}
	for cell, want := range cases {
		if got := ClassifyUsage(cell); got != want {
			t.Errorf("ClassifyUsage(%q) = %v, want %v", cell, got, want)
		}
	}
}

// Nothing measurable is not zero. A pod with no memory limit, a node whose
// kubelet did not answer and a claim the summary API had never heard of all
// arrive as a cell with no number in it, and painting those green would say
// there is plenty of room in exactly the case rta could not tell.
func TestACellWithNoNumberInItIsNotGradedAsEmpty(t *testing.T) {
	for _, cell := range []string{"", "  ", "—", "-", "n/a", "unknown", "%",
		"could not be read — the kubelet refused",
		// ParseFloat reads all of these and returns a nil error, so they used
		// to reach the band comparison. Every comparison against NaN is false,
		// which fell through to the comfortable band: "could not measure this"
		// was painted green, the one wrong answer a usage column must not give.
		"NaN%", "nan", "NAN%", "-Inf%", "Inf%", "+Infinity%"} {
		if got := ClassifyUsage(cell); got != StatusNeutral {
			t.Errorf("ClassifyUsage(%q) = %v, want Neutral", cell, got)
		}
	}
}

// Past a capacity is still the worst thing a capacity can say. A column where
// 150 is ordinary is one that must not declare KindUsage at all, and
// view.KindUsage's own comment lists the three in this codebase that do not.
func TestPastTheCapacityStaysTheWorstBand(t *testing.T) {
	for _, cell := range []string{"101%", "150%", "3400.0%"} {
		if got := ClassifyUsage(cell); got != StatusBad {
			t.Errorf("ClassifyUsage(%q) = %v, want Bad", cell, got)
		}
	}
}

// The boundaries are the contract's, not two numbers written in the renderer.
// `sys disk` puts "WARN >80%" in a Status column beside a Use% the renderer
// grades, and the two describing the same disk differently is the failure
// x509check exists to have stopped happening about certificates.
func TestTheBandsAreTheOnesTheContractStates(t *testing.T) {
	if ClassifyUsage(pct(view.UsageWarn-0.1)) != StatusGood {
		t.Error("just under the warn band is not comfortable")
	}
	if ClassifyUsage(pct(view.UsageWarn)) != StatusWarn {
		t.Error("the warn boundary is not inclusive")
	}
	if ClassifyUsage(pct(view.UsageBad-0.1)) != StatusWarn {
		t.Error("just under the bad band is not a warning")
	}
	if ClassifyUsage(pct(view.UsageBad)) != StatusBad {
		t.Error("the bad boundary is not inclusive")
	}
}

func pct(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64) + "%"
}
