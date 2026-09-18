package app

import (
	"os"
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

// A lock file that is gone while its seal key remains is not a clean
// machine: locks were removed (the documented recovery), a save failed
// after writing the key, or the key was truncated before anything was
// sealed. Each used to read "ok — none — nothing is frozen". Reported, never
// enforced — recoveryHint tells operators to remove the sealed file
// themselves, so refusing calls on this signal would turn documented
// recovery into an outage.
func TestDoctorNamesASealKeyLeftWithoutItsLockFile(t *testing.T) {
	isolate(t)
	l, verr := lockdown.Build("agent", "claude", "incident", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	if err := os.Remove(lockdown.Path()); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "locks", "info", "seal key")
}
