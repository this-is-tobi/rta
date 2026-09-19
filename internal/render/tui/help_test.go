package tui

import (
	"strings"
	"testing"
)

// `?` is the key a k9s or lazygit hand reaches for first, and the footer,
// however honest, is two lines: on a narrow terminal it has already dropped
// something and said so with "…", and it never has room for the aliases.
// The overlay lists every key the screen under it answers, from the same
// footerItems the bar packs — so the two cannot disagree — including what
// the bar had no room for.
func TestQuestionMarkListsEveryKeyTheScreenAnswers(t *testing.T) {
	m, _ := realModel(t, 40, 30)
	m.selected = 1
	if bar := plain(fitHintBar(m.width, footerMaxLines, m.footerItems(m.mode)...)); !strings.HasSuffix(strings.TrimSpace(bar), "…") {
		t.Fatalf("premise: a 40-column bar drops nothing:\n%s", bar)
	}
	items := m.footerItems(m.mode)

	m = press(t, m, "?")
	if !m.help {
		t.Fatal("? did not open the key overlay")
	}
	got := plain(m.View().Content)
	for _, it := range items {
		if !strings.Contains(got, it.display) || !strings.Contains(got, plain(it.label)) {
			t.Errorf("overlay is missing %q %q:\n%s", it.display, plain(it.label), got)
		}
	}
	if !strings.Contains(got, "dashboard") {
		t.Errorf("overlay does not name the screen it describes:\n%s", got)
	}
	// The aliases are the other half of what is discoverable here and nowhere
	// else: `q` also answers to ctrl+c on every screen, and the bar never
	// says so.
	if !strings.Contains(got, "ctrl+c") {
		t.Errorf("overlay does not list the aliases the bar hides:\n%s", got)
	}

	m = press(t, m, "esc")
	if m.help || m.mode != modeDashboard {
		t.Fatal("esc did not close the overlay onto the screen it came from")
	}
	if m = press(t, press(t, m, "?"), "?"); m.help {
		t.Fatal("? did not close the overlay it opened")
	}
}

// The overlay is offered exactly where a bare key is a command. A form, the
// theme editor and the copy picker type `?` into a field, and so does the
// dashboard's search bar while it holds the keyboard. The footer and the
// handler answer to one predicate, so a screen cannot advertise `?` and
// swallow it, or answer it unannounced.
func TestHelpIsOfferedWhereAKeyIsACommand(t *testing.T) {
	m, _ := realModel(t, 100, 40)
	for screen := modeDashboard; screen <= modeConfirm; screen++ {
		n := m
		n.mode = screen
		offered := n.helpOffered(screen)
		if advertises(n, "?") != offered {
			t.Errorf("%s: advertises ? = %v, offers it = %v", screenName(screen), advertises(n, "?"), offered)
		}
		if offered && !press(t, n, "?").help {
			t.Errorf("%s: advertises ? and does not answer it", screenName(screen))
		}
	}
	for _, screen := range []mode{modeForm, modeTheme, modeCopyPick} {
		if m.helpOffered(screen) {
			t.Errorf("%s offers help over a field that takes ? as text", screenName(screen))
		}
	}
	m.searchEditing = true
	if m.helpOffered(modeDashboard) || advertises(m, "?") || press(t, m, "?").help {
		t.Error("? reached the overlay while the search bar had the keyboard")
	}
}
