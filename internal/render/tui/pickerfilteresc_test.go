package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"

	"github.com/this-is-tobi/rta/internal/config"
)

// A form's picker gives esc to its filter first, and the add, copy and
// theme-owner pickers closed outright instead: somebody who pressed / to
// narrow the list and esc to undo the narrowing lost the whole screen. Each now
// clears a filter in force on the first esc and closes on the next.

var escKey = tea.KeyPressMsg{Code: tea.KeyEscape}

func TestEscInTheCopyPickersFilterClearsItAndTheNextCloses(t *testing.T) {
	m := filterFor(t, openPickerOnThreePasswords(t), "sec")
	if !filterHoldsEsc(m.copyPick.form) {
		t.Fatal("typing after / did not put a filter in force")
	}
	if bar := plain(m.footerFor(modeCopyPick)); !strings.Contains(bar, "esc clear") {
		t.Errorf("the bar does not say esc clears the filter:\n%s", bar)
	}
	m = tap(m, escKey)
	if bar := plain(m.footerFor(modeCopyPick)); !strings.Contains(bar, "esc cancel") {
		t.Errorf("the bar does not say esc cancels once the filter is gone:\n%s", bar)
	}
	if m.mode != modeCopyPick || m.copyPick == nil {
		t.Fatalf("esc with a filter in force closed the picker: mode %v", m.mode)
	}
	if filterHoldsEsc(m.copyPick.form) {
		t.Error("one esc left the filter in force")
	}
	m = tap(m, escKey)
	if m.mode != modeResult || m.copyPick != nil {
		t.Errorf("esc with no filter did not close the picker: mode %v", m.mode)
	}
}

func TestEscInTheThemeOwnerPickersFilterClearsItAndTheNextCloses(t *testing.T) {
	resetTheme(t)
	m := openTheme(t, profileModel(t, themedProfiles()))
	if m.themePick == nil {
		t.Fatal("the editor did not open on its picker")
	}
	m = filterFor(t, m, "sta")
	if !filterHoldsEsc(m.themePick.form) {
		t.Fatal("typing after / did not put a filter in force")
	}
	if bar := plain(m.footerFor(modeTheme)); !strings.Contains(bar, "esc clear") {
		t.Errorf("the bar does not say esc clears the filter:\n%s", bar)
	}
	m = tap(m, escKey)
	if m.mode != modeTheme || m.themePick == nil || filterHoldsEsc(m.themePick.form) {
		t.Fatalf("esc with a filter in force: mode %v, picker %v; want the picker, unfiltered", m.mode, m.themePick != nil)
	}
	m = tap(m, escKey)
	if m.mode != modeDashboard || m.themePick != nil {
		t.Errorf("esc with no filter did not close the picker: mode %v", m.mode)
	}
}

func TestEscInTheAddPickersFilterClearsItAndTheNextCloses(t *testing.T) {
	m := inCatalogue(t, addModel(t, twoInstanceConfig(), config.Dashboard{}), "db.slow")
	m, _ = plus(m)
	if m.mode != modeAddPick || m.addPick == nil {
		t.Fatalf("+ did not open the picker: mode %v", m.mode)
	}
	m = filterFor(t, m, "pro")
	if !filterHoldsEsc(m.addPick.form) {
		t.Fatal("typing after / did not put a filter in force")
	}
	if bar := plain(m.footerFor(modeAddPick)); !strings.Contains(bar, "esc clear") {
		t.Errorf("the bar does not say esc clears the filter:\n%s", bar)
	}
	m = tap(m, escKey)
	if m.mode != modeAddPick || m.addPick == nil || filterHoldsEsc(m.addPick.form) {
		t.Fatalf("esc with a filter in force: mode %v, picker %v; want the picker, unfiltered", m.mode, m.addPick != nil)
	}
	if m.addPick.form.State != huh.StateNormal {
		t.Errorf("the picker is %v after its filter was cleared", m.addPick.form.State)
	}
	m = tap(m, escKey)
	if m.mode == modeAddPick || m.addPick != nil {
		t.Errorf("esc with no filter did not close the picker: mode %v", m.mode)
	}
}
