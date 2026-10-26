package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The catalogue is a bubbles list, which carries a quit key of its own. rta
// quits on q and ctrl+c and goes back on esc, all before the list is asked;
// bubbles 2.1.1 bound the list's to q and esc, so nobody noticed it, and 2.2.1
// binds it to v, which nothing here handles, so a v in the catalogue ended the
// program with no word. Any key that does something the footer never says is
// caught by TestEveryKeyAScreenAnswersToIsAdvertised; this one is named so the
// next version that moves it is read as what it is.
func TestVInTheCatalogueDoesNotQuit(t *testing.T) {
	m, _ := realModel(t, 120, 40)
	m.mode = modeBrowse
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'v', Text: "v"})
	if cmd == nil {
		return
	}
	if _, quit := cmd().(tea.QuitMsg); quit {
		t.Error("v in the catalogue quits the program")
	}
}

// What the list's own quit binding is not allowed to take with it: ctrl+c
// while a filter is being typed belongs to the list, which quits on it.
func TestCtrlCStillQuitsWhileAFilterIsBeingTyped(t *testing.T) {
	m := filterFor(t, browsing(t), "dem")
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c while typing a filter does nothing")
	}
	if _, quit := cmd().(tea.QuitMsg); !quit {
		t.Error("ctrl+c while typing a filter does not quit")
	}
}
