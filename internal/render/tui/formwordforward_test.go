package tui

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A word-forward delete with the cursor on the last character of a single-line
// box moved the cursor one past the end of the value and read there, and "index
// out of range" ended the program with a stack trace on the terminal and
// whatever was typed lost. Every single-line box reached it: a field, the filter
// of a list of options, the catalogue's. bubbles 2.2.2 stops the search at the
// end of the value; until then rta did not send the three keys that spell it.
//
// Each spelling goes into each kind of box with the cursor on that last
// character, the one place it failed, and must take that character and only it.
// What is typed afterwards shows the box still takes input. The multi-line box's
// delete is another piece of code that never failed, and is asked the same so a
// writer gets one behaviour from every box.
func TestAWordForwardDeleteOnTheLastCharacterOfABoxTakesIt(t *testing.T) {
	boxes := map[string]plugin.FieldType{"single-line": plugin.String, "multi-line": plugin.Text}
	keys := map[string]tea.KeyPressMsg{
		"alt+d":       {Code: 'd', Mod: tea.ModAlt},
		"alt+delete":  {Code: tea.KeyDelete, Mod: tea.ModAlt},
		"ctrl+delete": {Code: tea.KeyDelete, Mod: tea.ModCtrl},
	}
	for box, typ := range boxes {
		c := plugin.Capability{
			ID: "demo.box", Summary: "s", Safety: plugin.Write,
			Inputs: []plugin.Field{{Name: "f", Type: typ}},
			Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil },
		}
		for name, key := range keys {
			t.Run(box+"/"+name, func(t *testing.T) {
				m := sizedForm(t, c, 100, 30)
				for _, k := range []string{"a", "b", "c", "left"} {
					m = press(t, m, k)
				}
				m = press(t, pressKey(t, m, key), "x")
				if got := m.form.values()["f"]; got != "abx" {
					t.Errorf("abc, left, %s, x in a %s box left %q, want abx", name, box, got)
				}
			})
		}
	}
}
