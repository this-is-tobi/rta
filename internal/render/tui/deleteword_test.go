package tui

import (
	"context"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The reason dropsKey exists, stated where it can stop being true: with the
// cursor before the last character, bubbles' text input moves one past the end
// of its value and reads there. The day this fails the panic is fixed
// upstream, and dropsKey (deleteword.go) and this test go.
func TestTheTextInputStillPanicsOnAWordForwardDelete(t *testing.T) {
	in := textinput.New()
	in.Focus()
	in.SetValue("abc")
	in.SetCursor(2)
	defer func() {
		if recover() == nil {
			t.Error("bubbles' text input no longer panics on a word-forward delete before its last " +
				"character: delete dropsKey in deleteword.go, and this test")
		}
	}()
	in.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
}

// A single-line box is sent none of the three spellings, so the program that
// ended on alt+d does nothing, and what was typed stays.
func TestAWordForwardDeleteInASingleLineBoxIsDroppedAndNothingIsLost(t *testing.T) {
	m := sizedForm(t, positionCap(plugin.Write, "a"), 100, 30)
	for _, r := range "abc" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "left")
	for _, k := range []tea.KeyPressMsg{
		{Code: 'd', Mod: tea.ModAlt},
		{Code: tea.KeyDelete, Mod: tea.ModAlt},
		{Code: tea.KeyDelete, Mod: tea.ModCtrl},
	} {
		next, _ := m.Update(k)
		m = next.(Model)
	}
	if !strings.Contains(plain(m.formView()), "abc") {
		t.Errorf("the box lost what was typed:\n%s", plain(m.formView()))
	}
}

// The multi-line box keeps its own: its word-forward delete is a different
// piece of code, and a writer who has it in their editor expects it.
func TestAWordForwardDeleteInAMultilineBoxStillDeletesTheWord(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.text", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "body", Type: plugin.Text}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil },
	}
	m := sizedForm(t, c, 100, 30)
	for _, r := range "alpha beta" {
		m = press(t, m, string(r))
	}
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyHome})
	m = pressKey(t, m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	got := plain(m.formView())
	if strings.Contains(got, "alpha") || !strings.Contains(got, "beta") {
		t.Errorf("alt+d at the start of the line did not delete the first word and only it:\n%s", got)
	}
}
