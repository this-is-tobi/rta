package tui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// An applied filter takes its box away, and with it the only place that said what
// the rows were narrowed by: the header read "9 of 131" over a blank row, and the
// bar said esc goes back when the first esc clears the filter and the second goes
// back.
func TestTheCatalogueSaysWhichFilterIsAppliedAndWhatEscDoesToIt(t *testing.T) {
	m := browsing(t)
	if bar := plain(m.browseFooter()); !strings.Contains(bar, "esc back") || strings.Contains(bar, "clear filter") {
		t.Fatalf("an unfiltered catalogue's bar:\n%s", bar)
	}

	typing := filterFor(t, m, "demo")
	applied := pump(t, typing, tea.KeyPressMsg{Code: tea.KeyEnter})
	if applied.list.FilterState() == typing.list.FilterState() {
		t.Fatal("enter did not apply the filter")
	}

	head := plain(applied.browseHeader(100))
	if !strings.Contains(head, "filter: demo · ") || !strings.Contains(head, " of ") {
		t.Errorf("the header does not name the applied filter:\n%s", head)
	}
	bar := plain(applied.browseFooter())
	if !strings.Contains(bar, "esc clear filter") || strings.Contains(bar, "esc back") {
		t.Errorf("the bar of a filtered catalogue:\n%s", bar)
	}

	cleared := pump(t, applied, tea.KeyPressMsg{Code: tea.KeyEscape})
	if cleared.mode != modeBrowse || strings.Contains(plain(cleared.browseHeader(100)), "filter:") {
		t.Errorf("the first esc left mode %v and the header %q", cleared.mode, plain(cleared.browseHeader(100)))
	}
	if bar := plain(cleared.browseFooter()); !strings.Contains(bar, "esc back") {
		t.Errorf("after the filter is cleared the bar reads:\n%s", bar)
	}
}

// A long term does not push the column headings off the line it shares with the
// count, and one with control characters in it is not drawn as they are.
func TestAFilterTermInTheCatalogueHeaderIsCutAndCleaned(t *testing.T) {
	m := browsing(t)
	m.list.SetFilterText(strings.Repeat("x", 80) + "\x1b[31m")
	head := plain(m.browseHeader(100))
	if strings.ContainsRune(head, '\x1b') {
		t.Errorf("the header carries an escape sequence: %q", head)
	}
	if !strings.Contains(head, "filter: "+strings.Repeat("x", 23)+"… · ") {
		t.Errorf("the term is not cut to 24 cells:\n%s", head)
	}
}

// The list pages and jumps to its ends on keys rta never wrote, and a catalogue of
// eight pages told nobody. The keys are on the bar and in the overlay.
func TestTheCatalogueBarAndOverlayTeachThePagingKeys(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m.mode = modeBrowse
	bar := plain(m.browseFooter())
	for _, want := range []string{"pgup/pgdn page", "home/end first/last"} {
		if !strings.Contains(bar, want) {
			t.Errorf("the bar lacks %q:\n%s", want, bar)
		}
	}
	m.help = true
	overlay := plain(m.View().Content)
	for _, want := range []string{"pgup/pgdn", "home/end"} {
		if !strings.Contains(overlay, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, overlay)
		}
	}
}

// The overlay lists the keys a display does not spell out, by whole name: the
// display "pgup/pgdn" holds the letters u and d, and the page keys u and d were
// left out of "also" for it.
func TestAKeyWhoseLetterIsInTheDisplayIsStillListedAsAnAlias(t *testing.T) {
	also := hiddenKeys(item(bindPage))
	for _, k := range []string{"u", "d", "b", "f", "h", "l", "pgdown"} {
		if !slices.Contains(also, k) {
			t.Errorf("%q is not listed among the aliases of %s: %v", k, bindPage.display, also)
		}
	}
	for _, k := range []string{"pgup"} {
		if slices.Contains(also, k) {
			t.Errorf("%q is spelled out by the display and listed again: %v", k, also)
		}
	}
}
