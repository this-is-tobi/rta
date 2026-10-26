package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A word back at the start of an empty multi-line box looked for a word before
// the first character and never came back. The form is the terminal's whole
// event loop, so no key answered again, esc included, and the process had to be
// killed from another shell. bubbles 2.2.1 stops at the start of the input.
//
// The key goes in with a deadline, since a hang is the one failure a table of
// expectations cannot state, and what is typed afterwards shows the form still
// takes input.
func TestAWordBackAtTheStartOfAnEmptyMultilineBoxReturns(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.text", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "body", Type: plugin.Text}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ran"}, nil },
	}
	keys := map[string]tea.KeyPressMsg{
		"alt+b":     {Code: 'b', Mod: tea.ModAlt},
		"alt+left":  {Code: tea.KeyLeft, Mod: tea.ModAlt},
		"ctrl+left": {Code: tea.KeyLeft, Mod: tea.ModCtrl},
	}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			m := sizedForm(t, c, 100, 30)
			done := make(chan Model, 1)
			go func() { done <- pressKey(t, m, key) }()
			select {
			case m = <-done:
			case <-time.After(5 * time.Second):
				t.Fatalf("%s at the start of an empty multi-line box did not return: the form is frozen", name)
			}
			for _, r := range "afterwards" {
				m = press(t, m, string(r))
			}
			if !strings.Contains(plain(m.formView()), "afterwards") {
				t.Errorf("the box takes no text after %s:\n%s", name, plain(m.formView()))
			}
		})
	}
}
