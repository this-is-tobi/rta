package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A form's refusal says what is wrong and, at its end, what to do about it:
// `rta plugin install pg installs it`. The bar cut every message at the width
// of the terminal, which is where the remedy was, and left `esc cancel` on a
// row of its own whenever the hints before it filled the first.

const installRefusal = `profile names "pg", which is not a registered plugin — ` +
	`pg is a first-party plugin — rta plugin install pg installs it`

// barRows is the footer of the form as the rows it is drawn in.
func barRows(m Model) []string {
	return strings.Split(plain(m.footerFor(modeForm)), "\n")
}

// said is a bar read as running text, with the row breaks and the hanging
// indent of a wrapped sentence closed up.
func said(rows []string) string { return strings.Join(strings.Fields(strings.Join(rows, " ")), " ") }

func formAt(t *testing.T, w int) Model {
	t.Helper()
	return sizedForm(t, positionCap(plugin.Write, "a", "b"), w, 24)
}

func TestARefusalIsWrappedWhereItWasCutAndKeepsItsRemedy(t *testing.T) {
	for _, w := range []int{120, 80, 60, 44} {
		m := formAt(t, w)
		m.refuse(installRefusal)
		rows := barRows(m)
		if len(rows) > footerMaxLines {
			t.Errorf("width %d: the bar is %d rows, want at most %d:\n%s", w, len(rows), footerMaxLines, strings.Join(rows, "\n"))
		}
		for _, r := range rows {
			if got := lipgloss.Width(r); got > w {
				t.Errorf("width %d: a row is %d cells wide: %q", w, got, r)
			}
		}
		text := said(rows)
		if !strings.Contains(text, "esc cancel") {
			t.Errorf("width %d: the bar no longer says how to leave:\n%s", w, strings.Join(rows, "\n"))
		}
		if !strings.Contains(text, "rta plugin install pg installs it") {
			t.Errorf("width %d: the remedy is not readable:\n%s", w, strings.Join(rows, "\n"))
		}
		if w >= 80 && !strings.Contains(text, installRefusal) {
			t.Errorf("width %d: a message that fits in the bar was cut:\n%s", w, strings.Join(rows, "\n"))
		}
		if !strings.Contains(strings.TrimSpace(rows[0]), "✗ ") {
			t.Errorf("width %d: the refusal does not lead the bar:\n%s", w, strings.Join(rows, "\n"))
		}
	}
}

// What cannot fit is cut from the middle, once, and said so: the start says
// what went wrong and the end says what to do.
func TestARefusalTooLongForTheBarLosesItsMiddleAndSaysSo(t *testing.T) {
	m := formAt(t, 44)
	m.refuse(installRefusal)
	text := said(barRows(m))
	if sentence, _, _ := strings.Cut(text, "installs it"); strings.Count(sentence, "…") != 1 {
		t.Errorf("want one ellipsis where the middle went:\n%s", text)
	}
	if !strings.Contains(text, "✗ profile names") {
		t.Errorf("the start of the refusal is gone:\n%s", text)
	}
}

// The hints go before the message does, lowest ranked first, and how to leave
// outlasts them all.
func TestHintsGiveWayToARefusalAndHowToLeaveIsTheLastToGo(t *testing.T) {
	m := formAt(t, 80)
	m.refuse(installRefusal)
	bar := plain(m.footerFor(modeForm))
	if !strings.Contains(bar, "esc cancel") {
		t.Errorf("how to leave was dropped before the fast key:\n%s", bar)
	}
	if strings.Contains(bar, "ctrl+s submit") {
		t.Errorf("the fast key stayed while the room was the refusal's:\n%s", bar)
	}
	if !strings.Contains(bar, "…") {
		t.Errorf("hints were dropped and the bar does not say so:\n%s", bar)
	}
}

// A validator's message gets the same room, and it leads, ahead of the hints.
func TestAValidationErrorLeadsTheBarAndIsNotCut(t *testing.T) {
	c := positionCap(plugin.Write, "a", "b")
	cf := newCapForm(c, c.Inputs, nil, true, nil,
		withCheck("a", func(string) error { return errString(installRefusal) }))
	m := formModel(t, cf)
	m.current, m.width, m.height = c, 44, 24
	m.fitForm()
	m = press(t, m, "x")
	m = pressEnter(t, m)

	rows := barRows(m)
	if len(rows) > footerMaxLines {
		t.Errorf("the bar is %d rows:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.HasPrefix(strings.TrimSpace(rows[0]), "✗") {
		t.Errorf("the error does not lead the bar:\n%s", strings.Join(rows, "\n"))
	}
	if !strings.Contains(said(rows), "rta plugin install pg installs it") {
		t.Errorf("the remedy is not readable:\n%s", strings.Join(rows, "\n"))
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// A short message costs what it did: one phrase among the hints.
func TestAShortErrorStaysAmongTheHints(t *testing.T) {
	m := formAt(t, 100)
	m = pressEnter(t, m)
	rows := barRows(m)
	if len(rows) != 1 {
		t.Fatalf("a short error and the hints took %d rows:\n%s", len(rows), strings.Join(rows, "\n"))
	}
	if !strings.HasPrefix(strings.TrimSpace(rows[0]), "✗ a is required") || !strings.Contains(rows[0], "esc cancel") {
		t.Errorf("bar = %q, want the error, then the hints", rows[0])
	}
}

// `esc cancel` on a row of its own reads as a different kind of thing from the
// keys above it. A bar of two rows shares the hints between them.
func TestHowToLeaveIsNotLeftAloneOnTheSecondRow(t *testing.T) {
	m := openFormOn(t, New(fastFormRegistry(t, completingCap()), config.Dashboard{}, nil), completingCap())
	m.current, m.width, m.height = completingCap(), 80, 24
	m.fitForm()
	for _, w := range []int{80, 60, 44} {
		m.width = w
		rows := barRows(m)
		if len(rows) != 2 {
			t.Fatalf("width %d: the bar is %d rows, the test wants two:\n%s", w, len(rows), strings.Join(rows, "\n"))
		}
		if strings.TrimSpace(rows[1]) == "esc cancel" {
			t.Errorf("width %d: esc cancel is alone on the second row:\n%s", w, strings.Join(rows, "\n"))
		}
	}
}

// Every other bar is laid out as it was.
func TestOtherBarsKeepTheirLayout(t *testing.T) {
	items := []hintItem{
		action("a", "add"), item(bindSelect), item(bindMove), item(bindHide),
		item(bindPlugin), item(bindBrowse), item(bindSearch), item(bindQuit),
	}
	bar := plain(fitHintBar(70, footerMaxLines, items...))
	if rows := strings.Split(bar, "\n"); len(rows) != 2 || strings.TrimSpace(rows[1]) != "/ search · q quit" {
		t.Errorf("the greedy layout changed:\n%s", bar)
	}
}
