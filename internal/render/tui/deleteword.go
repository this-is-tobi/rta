package tui

import (
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// dropsKey is whether a key is one the screen on show must not be sent.
//
// **Delete-word-forward panics bubbles' text input.** With the cursor on the
// last character of a single-line box, textinput.deleteWordForward moves the
// cursor one past the end and reads the value there, and "index out of range"
// ends the program with a stack trace on the terminal and whatever was typed
// lost: type "abc", press left, then alt+d. The code is the same in 2.1.1 and
// in 2.2.1, and every single-line box reaches it: a field, the filter of a
// list of options, the catalogue's. huh keeps its input's key map to itself, so
// the binding cannot be turned off where it lives; the three keys that spell it
// are not sent instead. A multi-line box has a word-forward delete of its own
// that does not fail, and keeps them.
//
// TestTheTextInputStillPanicsOnAWordForwardDelete fails once bubbles stops
// doing this, which is the time to delete this.
func (m Model) dropsKey(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "alt+d", "alt+delete", "ctrl+delete":
		return !m.multilineFocused()
	}
	return false
}

// multilineFocused is whether the form on screen has a multi-line box under
// the cursor.
func (m Model) multilineFocused() bool {
	if m.mode != modeForm || m.form == nil || m.form.form == nil {
		return false
	}
	_, multiline := m.form.form.GetFocusedField().(*huh.Text)
	return multiline
}
