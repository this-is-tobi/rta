package app

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/lockdown"
)

// A lock was visible on `rta lock list` and nowhere else. A health check
// that lists standing grants and not the thing that overrides them was
// answering the smaller question.
func TestDoctorNamesWhatIsLocked(t *testing.T) {
	isolate(t)
	check(t, report(t), "locks", "ok", "none")
	l, verr := lockdown.Build("agent", "claude", "incident", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	check(t, report(t), "locks", "info", "claude")
}
