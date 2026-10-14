package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/lockdown"
)

// `L` on a queue row seeds the name from the row, and a form does not ask for
// what it was given, so the lock form that opened said nothing of whom it was
// about. It held kind, all, note and ttl, and a person in a hurry pressed
// ctrl+s over whichever row the cursor happened to be on. The `A` form beside
// it has always said which call it answers.
func TestTheLockFormNamesWhomItWillLock(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	form := press(t, screenOf(t, "agent.pending", nil), "L")
	if form.mode != modeForm || form.current.ID != lockCapability {
		t.Fatalf("L left %v on %q, want the lock form", form.mode, form.current.ID)
	}
	if formFields(form)["name"] {
		t.Fatalf("the form asks for the name the row gave it: %v", formFields(form))
	}
	if got := form.formAbout(); got != "lock agent claude" {
		t.Errorf("the form is about %q, want the agent the row named", got)
	}
	if title := plain(form.formView()); !strings.Contains(title, "lock agent claude") {
		t.Errorf("the form's title does not say whom it locks:\n%s", firstLine(title))
	}
}

// The kind is one of the boxes, and the title follows it: one that went on
// saying "agent" after the box was moved to credential would be a line that
// lies on the one screen added to be trusted.
func TestTheLockFormTitleFollowsTheKindBox(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	form := press(t, screenOf(t, "agent.pending", nil), "L")
	bound, ok := form.form.bindings["kind"]
	if !ok {
		t.Fatalf("the form has no kind box: %v", formFields(form))
	}
	*bound = string(lockdown.KindCredential)
	if got := form.formAbout(); got != "lock credential claude" {
		t.Errorf("with the kind moved to credential the form is about %q", got)
	}
}

// A form that asks for the name itself has it on screen: its title stays the
// capability's own summary.
func TestTheLockFormThatAsksForTheNameKeepsItsSummary(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	opened, _ := m.startForm(mustCap(t, m.reg, lockCapability), nil)
	form := opened.(Model)
	if !formFields(form)["name"] {
		t.Fatalf("the form does not ask for the name: %v", formFields(form))
	}
	if got := form.formAbout(); got != "" {
		t.Errorf("the form is about %q although it shows the name box", got)
	}
}

// The footer after a lock said "lock.add done", and the row that was locked was
// known only to `rta lock list`. It says what the capability answered.
func TestTheFooterSaysWhoWasLocked(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	form := press(t, screenOf(t, "agent.pending", nil), "L")
	done := pump(t, form, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !strings.Contains(done.flash, "locked agent claude") || strings.Contains(done.flash, "lock.add done") {
		t.Errorf("the footer says %q after the lock, want whom it locked", done.flash)
	}
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 || locks[0].Name != "claude" {
		t.Errorf("the lock the footer reports is not the one on disk: %v %v", locks, verr)
	}
}
