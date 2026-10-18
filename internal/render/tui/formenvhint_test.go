package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The environment picker and the entry that states a connection for one call
// sit above the first box of a run form. With no environment configured the
// cursor starts below them, and nothing on the screen said they were there: up
// from the first box does nothing, and the way up is shift+tab.

var shiftTab = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

func TestTheFooterNamesTheKeyThatReachesTheEnvironmentPicker(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	m = pastThePicker(t, openFormOn(t, m, capNamed(t, m, "pgx.status")))

	if bar := plain(m.footerFor(modeForm)); !strings.Contains(bar, "⇧tab environment") {
		t.Fatalf("below the picker the bar does not say how to reach it:\n%s", bar)
	}

	back := pressKey(t, m, shiftTab)
	if bar := plain(back.footerFor(modeForm)); strings.Contains(bar, "⇧tab environment") {
		t.Errorf("on the picker the bar still points at it:\n%s", bar)
	}
}

// On the picker itself there is nothing above, and a form whose plugin has no
// picker has nothing to point at.
func TestTheFooterPointsAtNoPickerThereIsNone(t *testing.T) {
	m := adHocModel(t, configOnlyPlugin())
	m = openFormOn(t, m, capNamed(t, m, "cfgonly.go"))
	if bar := plain(m.footerFor(modeForm)); strings.Contains(bar, "⇧tab") {
		t.Errorf("a form with no picker points at one:\n%s", bar)
	}

	own := sizedForm(t, positionCap(plugin.Write, "a", "b"), 100, 24)
	if bar := plain(own.footerFor(modeForm)); strings.Contains(bar, "⇧tab") {
		t.Errorf("a form of the capability's own boxes points at a picker:\n%s", bar)
	}
}

// The hint is true: shift+tab from the first box under the picker is the move
// that lands on it.
func TestShiftTabFromTheFirstBoxLandsOnThePicker(t *testing.T) {
	m := adHocModel(t, adHocPlugin(nil))
	m = pastThePicker(t, openFormOn(t, m, capNamed(t, m, "pgx.status")))
	if focused := m.form.form.GetFocusedField(); focused == m.form.widgets[0] {
		t.Fatal("the cursor is on the picker already")
	}
	back := pressKey(t, m, shiftTab)
	if back.form.form.GetFocusedField() != back.form.widgets[0] {
		t.Errorf("shift+tab left the cursor off the picker")
	}
}
