package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A form trimmed every box, a secret's included, where the command line and an
// agent's call keep a value exactly: `  s3cr3t ` set from the TUI was stored as
// `s3cr3t`, and a store whose passphrase was chosen at the prompt with a space
// at an end could never be unlocked from the TUI. A secret is what was typed;
// every other box is still read without the space around it.
func TestASecretBoxAnswersWhatWasTypedSpaceIncluded(t *testing.T) {
	c := plugin.Capability{ID: "kv.set", Summary: "set", Safety: plugin.Write, Inputs: []plugin.Field{
		{Name: "key", Type: plugin.String, Required: true, Help: "k"},
		{Name: "value", Type: plugin.Secret, Required: true, Help: "v"},
		{Name: "tokens", Type: plugin.SecretSlice, Help: "t"},
	}}
	cf := newCapForm(c, c.Inputs, nil, true, nil)
	*cf.bindings["key"] = "  api "
	*cf.bindings["value"] = "  s3cr3t "
	*cf.bindings["tokens"] = " a, b "
	got := cf.values()
	if got["value"] != "  s3cr3t " {
		t.Errorf("the secret answered %q, want what was typed", got["value"])
	}
	if got["key"] != "api" {
		t.Errorf("the name answered %q, want it without the space around it", got["key"])
	}
	// A list of secrets is read by the list grammar, which decides about the
	// space at an element's ends, as the CLI's list flag does.
	if tokens, _ := got["tokens"].([]string); len(tokens) != 2 || tokens[0] != "a" || tokens[1] != "b" {
		t.Errorf("the list of secrets answered %q, want its two elements", got["tokens"])
	}

	*cf.bindings["value"] = "   "
	if v, ok := cf.values()["value"]; ok {
		t.Errorf("a secret box holding only space answered %q, want nothing", v)
	}
}

// secretForm is a form of one masked box, open and focused on it.
func secretForm(t *testing.T) Model {
	t.Helper()
	noHistory(t)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	c := plugin.Capability{
		ID: "demo.secret", Summary: "a write with a secret", Safety: plugin.Write,
		Inputs: []plugin.Field{{Name: "value", Type: plugin.Secret, Required: true, Help: "v"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{Body: "ok"}, nil },
	}
	model, _ := New(fastFormRegistry(t, c), config.Dashboard{}, nil).startForm(c, nil)
	m := model.(Model)
	m.form.form = startedForm(m.form)
	m.current = c
	return m
}

// A line copied from a terminal, or put on the clipboard from a file, comes
// with the line's own newline, and the box's text input turns a line ending
// into a space. The trim every box once had took that space away; a secret
// keeps its edges now, and the same paste was saved as `s3cr3t ` where `rta kv
// set`'s prompt, which reads one line, saves `s3cr3t`. The paste's own line
// ending is not part of the value, and nothing else is taken: a space that is
// the secret's last character, typed or pasted, is kept.
func TestASecretPastedWithItsLineEndingAnswersWithoutIt(t *testing.T) {
	cases := map[string]struct{ paste, want string }{
		"a newline":                    {"s3cr3t\n", "s3cr3t"},
		"a carriage return":            {"s3cr3t\r", "s3cr3t"},
		"a carriage return and a feed": {"s3cr3t\r\n", "s3cr3t"},
		"a space that is the secret's": {"s3cr3t \r\n", "s3cr3t "},
		"no line ending":               {"s3cr3t ", "s3cr3t "},
		"blank lines after it":         {"s3cr3t\n\n", "s3cr3t"},
		"a line ending inside it":      {"s3\ncr3t\n", "s3 cr3t"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			m := tap(secretForm(t), tea.PasteMsg{Content: c.paste})
			if got := m.form.values()["value"]; got != c.want {
				t.Errorf("a pasted %q answers %q, want %q", c.paste, got, c.want)
			}
		})
	}

	t.Run("typed", func(t *testing.T) {
		m := strike(secretForm(t), "s3cr3t ")
		if got := m.form.values()["value"]; got != "s3cr3t " {
			t.Errorf("a typed secret ending in a space answers %q, want it kept", got)
		}
	})
}
