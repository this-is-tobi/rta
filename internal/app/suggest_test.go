package app

import (
	"strings"
	"testing"
)

// The root command has always answered a typo with cobra's "Did you mean
// this?"; every group below it lost that the day it grew a RunE of its own,
// so `rta sy cpu` suggested `sys` while `rta sys cpuu` only said unknown.
// One level down is where the typos actually happen.
func TestAnUnknownVerbBelowTheRootSuggestsTheNearest(t *testing.T) {
	_, _, err := run(t, testRegistry(t), "demo", "item", "lst")
	if err == nil {
		t.Fatal("`demo item lst` succeeded")
	}
	if !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("error does not say the command is unknown: %v", err)
	}
	if !strings.Contains(err.Error(), "Did you mean this?") || !strings.Contains(err.Error(), "list") {
		t.Errorf("error does not suggest `list`:\n%v", err)
	}
	if got := ExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2 (usage)", got)
	}
}
