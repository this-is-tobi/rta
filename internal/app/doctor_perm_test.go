package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The data directory was created world-listable until 0.11.0, and a
// tighter mode on creation does nothing for a machine that already has
// one. Doctor is where an operator finds out.
func TestDoctorSaysWhenTheDataDirectoryIsListable(t *testing.T) {
	dataDir, _ := isolate(t)
	if err := os.Chmod(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "data", "warn", "chmod 700 "+dataDir)
	if err := os.Chmod(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "data", "ok", dataDir)
}

func TestDoctorNamesADataDirectoryThatDoesNotExistYet(t *testing.T) {
	dataDir, _ := isolate(t)
	if err := os.Remove(dataDir); err != nil {
		t.Fatal(err)
	}
	check(t, report(t), "data", "ok", "nothing written yet")
}

// **A data directory rta cannot write is the first thing doctor says, and it
// says it once.** It read `ok (nothing written yet)` over a directory whose
// parent belonged to root, and the agent log row beside it said the log was
// busy, which it was not.
func TestDoctorFailsTheDataRowOnADirectoryNobodyCanWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write a directory whatever its mode")
	}
	dataDir, _ := isolate(t)
	if err := os.Chmod(dataDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dataDir, 0o700) })

	rows := report(t)
	check(t, rows, "data", "error", "cannot write to its data directory "+dataDir)
	check(t, rows, "data", "error", "RTA_DATA_DIR")
	if got := rows["agent log"]; strings.Contains(got[1], "busy") || strings.Contains(got[1], "cannot write to its data directory") {
		t.Errorf("the agent log row repeats the data row: %q", got[1])
	}

	missing := filepath.Join(dataDir, "rta")
	t.Setenv("RTA_DATA_DIR", missing)
	check(t, report(t), "data", "error", "cannot create its data directory "+missing)
}
