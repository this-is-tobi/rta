package tui

import (
	"strings"
	"testing"
)

// Every flash wore the green check mark of a success, so "config not saved:
// permission denied" read as the confirmation of the save beside it. A refusal
// is drawn with the cross a form's error gets, and the mark follows the text:
// what replaces a refusal is a different string and is drawn as itself.
func TestAFlashThatReportsAFailureIsNotMarkedAsASuccess(t *testing.T) {
	m, _ := realModel(t, 120, 40)

	m.refuse("config not saved: permission denied")
	bar := plain(m.footerFor(m.mode))
	if !strings.Contains(bar, "✗ config not saved: permission denied") || strings.Contains(bar, "✓") {
		t.Errorf("a refusal in the footer:\n%s\nwant the cross and no check mark", bar)
	}

	m.flash = "saved profile prod"
	bar = plain(m.footerFor(m.mode))
	if !strings.Contains(bar, "✓ saved profile prod") || strings.Contains(bar, "✗") {
		t.Errorf("a confirmation after a refusal:\n%s\nwant the check mark and no cross", bar)
	}
}
