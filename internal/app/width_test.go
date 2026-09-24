package app

import (
	"strings"
	"testing"
)

// COLUMNS shapes the output wherever it goes, a pipe included, and anything
// that is not a positive number is no request at all — which on a pipe leaves
// the natural width a script depends on. Nor is a width no terminal could
// report: the renderer pads out to it, so an unbounded one is an allocation
// sized by an environment variable.
func TestTermWidthTakesColumnsWhenItNamesAWidth(t *testing.T) {
	if isTTY() {
		t.Skip("stdout is a terminal here, so the fallback is its width rather than 0")
	}
	for in, want := range map[string]int{
		"72": 72, "": 0, "0": 0, "-5": 0, "wide": 0,
		"65535": 65535, "65536": 0, "10000000000": 0,
	} {
		t.Setenv("COLUMNS", in)
		if got := termWidth(); got != want {
			t.Errorf("COLUMNS=%q: width = %d, want %d", in, got, want)
		}
	}
}

// doctor names COLUMNS when it is set, because it moves every table's layout
// from somewhere nobody looks.
func TestDoctorSaysWhenColumnsShapesTheOutput(t *testing.T) {
	var detail string
	add := func(check, _, d string) {
		if check == "terminal" {
			detail = d
		}
	}
	t.Setenv("COLUMNS", "")
	doctorTerminal(add)
	if strings.Contains(detail, "COLUMNS") {
		t.Errorf("an unset COLUMNS was reported: %q", detail)
	}
	t.Setenv("COLUMNS", "90")
	doctorTerminal(add)
	if !strings.HasSuffix(detail, "shaped to COLUMNS=90") {
		t.Errorf("terminal detail = %q, want it to name COLUMNS=90", detail)
	}
}
