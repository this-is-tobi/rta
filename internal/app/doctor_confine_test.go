package app

import (
	"runtime"
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
	rows := report(t)
	check(t, rows, "plugin confinement", "ok", "denied read+write")
	check(t, rows, "plugin confinement", "ok", "own directory under the store")
	// Reads, and the row has to say so: the write half of that denial is what
	// stopped a blind overwrite of the grant file.
	check(t, rows, "plugin confinement", "ok", "reads only")
}
