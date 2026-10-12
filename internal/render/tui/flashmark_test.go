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

// "pick a row, then d done" is guidance, and wore the check of a success: it
// read as the confirmation of a press that had only opened a list. It has a
// mark of its own, and what replaces it is drawn as itself.
func TestAFlashThatOnlyGuidesIsNotMarkedAsASuccess(t *testing.T) {
	m, _ := realModel(t, 120, 40)

	m.guide("pick a row, then d done")
	bar := plain(m.footerFor(m.mode))
	if !strings.Contains(bar, "→ pick a row, then d done") || strings.Contains(bar, "✓ pick") || strings.Contains(bar, "✗ pick") {
		t.Errorf("guidance in the footer:\n%s\nwant its own mark and neither the check nor the cross", bar)
	}

	m.flash = "marked done"
	bar = plain(m.footerFor(m.mode))
	if !strings.Contains(bar, "✓ marked done") || strings.Contains(bar, "→ marked done") {
		t.Errorf("a confirmation after guidance:\n%s\nwant the check mark", bar)
	}
}
