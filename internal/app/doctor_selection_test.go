package app

import (
	"os"
	"testing"

	"github.com/this-is-tobi/rta/internal/profile"
)

// The fence round the environments is held shut while the file that records it
// does not verify, which from an agent reads as grants that stopped working.
// Doctor is where it is named, with the way out.
func TestDoctorReportsASelectionFileThatDoesNotVerify(t *testing.T) {
	isolate(t)
	rows := report(t)
	if _, found := rows["profile selection"]; found {
		t.Fatalf("a machine with no selection reports one: %v", rows["profile selection"])
	}

	if verr := profile.SaveSelection(profile.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	if _, found := report(t)["profile selection"]; found {
		t.Fatal("a selection rta wrote is reported as one that does not verify")
	}

	if err := os.WriteFile(profile.SelectionPath(), []byte(`{"active":"prod"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "profile selection", "error", "rta use")
}
