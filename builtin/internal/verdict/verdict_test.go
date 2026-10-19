package verdict

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/render/theme"
)

func TestAPageThatFoundNothingIsOk(t *testing.T) {
	var v Verdict
	if got := v.String(); got != "ok" || theme.ClassifyStatus(got) != theme.StatusGood {
		t.Errorf("verdict = %q", got)
	}
}

func TestTheWorstThingFoundIsTheVerdictAndTheFirstOfTwoEquals(t *testing.T) {
	var v Verdict
	v.Warn("2 calls waiting on you")
	v.Warn("locks could not be read")
	if got := v.String(); got != "warn — 2 calls waiting on you" {
		t.Errorf("verdict = %q, want the first warning", got)
	}
	v.Fail("recording cannot be written")
	v.Fail("something later and as bad")
	v.Warn("a warning after a failure")
	if got := v.String(); got != "fail — recording cannot be written" {
		t.Errorf("verdict = %q, want the first failure over every warning", got)
	}
}

// The words lead the line, and the theme draws each as what it is.
func TestTheWordsAreTheOnesTheThemeGrades(t *testing.T) {
	var warned, failed Verdict
	warned.Warn("x")
	failed.Fail("x")
	if got := theme.ClassifyStatus(warned.String()); got != theme.StatusWarn {
		t.Errorf("%q is drawn as %v", warned.String(), got)
	}
	if got := theme.ClassifyStatus(failed.String()); got != theme.StatusBad {
		t.Errorf("%q is drawn as %v", failed.String(), got)
	}
}
