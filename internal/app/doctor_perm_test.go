package app

import (
	"os"
	"runtime"
	"testing"
)

// The data directory was created world-listable until 0.11.0, and a
// tighter mode on creation does nothing for a machine that already has
// one. Doctor is where an operator finds out.
func TestDoctorSaysWhenTheDataDirectoryIsListable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix mode bits on windows")
	}
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
	check(t, report(t), "data", "info", "nothing written yet")
}
