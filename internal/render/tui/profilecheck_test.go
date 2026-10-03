package tui

import (
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

func profileEditor(t *testing.T) *huh.Form {
	t.Helper()
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	m, _ := realModel(t, 100, 30)
	next, _ := m.startProfileForm("")
	cf := next.(Model).form
	if cf == nil {
		t.Fatal("the editor did not open")
	}
	return cf.form
}

func enter(f *huh.Form) *huh.Form { return settleForm(f, tea.KeyPressMsg{Code: tea.KeyEnter}) }

// A profile's name and colour were checked only at the save, after the last
// box: the editor closed to the pane behind it and said so in a footer line the
// form was no longer there to be corrected from, and everything typed was
// gone. They are checked at the box now, in the footer beside the key, and the
// form stays.
func TestTheProfileEditorHoldsANameAtTheBox(t *testing.T) {
	for _, bad := range []string{"Bad Name", "-edge", "a_b"} {
		f := enter(typeInto(profileEditor(t), bad))
		if f.State != huh.StateNormal || len(f.Errors()) == 0 || !strings.Contains(f.Errors()[0].Error(), "not a valid profile name") {
			t.Errorf("name %q: state %v, errors %v, want the form to stay with the name refused", bad, f.State, f.Errors())
		}
	}
	f := enter(typeInto(profileEditor(t), "staging"))
	if len(f.Errors()) != 0 {
		t.Errorf("a good name was refused: %v", f.Errors())
	}
}

func TestTheProfileEditorHoldsAColourAtTheBox(t *testing.T) {
	// name, note and ttl are passed; the colour is the last box.
	toColour := func() *huh.Form {
		f := enter(typeInto(profileEditor(t), "staging"))
		f = enter(f)
		return enter(f)
	}
	f := enter(typeInto(toColour(), "red"))
	if f.State != huh.StateNormal || len(f.Errors()) == 0 || !strings.Contains(f.Errors()[0].Error(), "red is not a colour") {
		t.Errorf("colour red: state %v, errors %v, want the form to stay with the colour refused", f.State, f.Errors())
	}
	if f := enter(typeInto(toColour(), "#ff8800")); f.State != huh.StateCompleted {
		t.Errorf("a good colour: state %v, errors %v", f.State, f.Errors())
	}
}
