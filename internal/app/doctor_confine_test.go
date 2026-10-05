package app

import (
	"runtime"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/pluginhost"
)

// The confinement row is what docs/40-plugins tells people to read instead of
// assuming what is denied, so the one readable place inside rta's own state
// has to be in the row and not only in the chapter beside it. A report
// stating a denial the launch then relaxes is drift in the direction that
// overstates what is protected.
func TestDoctorNamesTheOneReadablePlaceInsideItsOwnState(t *testing.T) {
	if !pluginhost.Confined() {
		t.Skipf("no confinement on %s, so the row says so instead", runtime.GOOS)
	}
	isolate(t)
	rows := reportFor(t, true)
	check(t, rows, "plugin confinement", "ok", "denied read+write")
	check(t, rows, "plugin confinement", "ok", "own directory under the store")
	// Reads, and the row has to say so: the write half of that denial is what
	// stopped a blind overwrite of the grant file.
	check(t, rows, "plugin confinement", "ok", "reads only")
}

// Without --detail the row is one sentence, and it must not read as a bound
// without its exception: every number stays, "everything else is readable"
// stays, and what it leaves out is said to be left out and where it is.
func TestTheConfinementSummaryKeepsTheBoundAndPointsAtTheException(t *testing.T) {
	deny := pluginhost.DenySet{NoAccess: []string{"/a", "/b"}, NoRead: []string{"/c"}, NoMove: []string{"/", "/e"}}
	got := sandboxDetail(deny, false)
	for _, want := range []string{
		"2 paths denied read+write", "1 denied read", "2 directories pinned",
		"everything else is readable", "one exception", "--detail",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the summary does not say %q: %s", want, got)
		}
	}
	if strings.Contains(got, "certificate") {
		t.Errorf("the summary carries the reason for the exception, which is --detail's: %s", got)
	}
	if len(got) > 150 {
		t.Errorf("the summary is %d bytes, which is a paragraph and not a line: %s", len(got), got)
	}
}

// Each of the row's counts agrees with its noun, one included: a deny set is
// the machine's, and one with a single entry of a kind said 1 paths.
func TestTheConfinementRowCountsInTheRightNumber(t *testing.T) {
	one := pluginhost.DenySet{NoAccess: []string{"/a"}, NoRead: []string{"/b"}, NoMove: []string{"/"}}
	got := sandboxDetail(one, true)
	for _, want := range []string{
		"1 path denied read+write", "1 denied read (a credential location)", "1 directory pinned",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the row does not say %q: %s", want, got)
		}
	}
	two := pluginhost.DenySet{NoAccess: []string{"/a", "/b"}, NoRead: []string{"/c", "/d"},
		NoMove: []string{"/", "/e"}}
	got = sandboxDetail(two, true)
	for _, want := range []string{
		"2 paths denied read+write", "2 denied read (credential locations)", "2 directories pinned",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the row does not say %q: %s", want, got)
		}
	}
}
