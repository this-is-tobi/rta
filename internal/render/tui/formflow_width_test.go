package tui

import "testing"

// The form's measure. A cap of 80 made a one-line field description wrap in
// the middle of a 190-column terminal with the rest of the window empty
// beside it, which reads as a layout bug because it is one.
func TestFormWidthUsesTheRoomItIsGivenUpToAReadableBound(t *testing.T) {
	for _, tc := range []struct {
		name string
		term int
		want int
	}{
		// A narrow terminal gets what is left after the panel's chrome, the
		// same as it always did.
		{"narrow", 60, 54},
		{"just under the bound", 100, 94},
		// Past the bound, more room stops being an improvement: measure is a
		// readability property, not a space-filling one.
		{"wide", 190, 120},
		{"very wide", 300, 120},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := formWidth(tc.term); got != tc.want {
				t.Fatalf("formWidth(%d) = %d, want %d", tc.term, got, tc.want)
			}
		})
	}
	// The regression itself: the old cap. A 190-column terminal must not be
	// treated the same as an 86-column one.
	if formWidth(190) <= formWidth(86) {
		t.Fatalf("a wide terminal gets no more form than a narrow one (%d vs %d)",
			formWidth(190), formWidth(86))
	}
}
