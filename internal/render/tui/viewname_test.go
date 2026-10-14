package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
)

// A view's name is a key in a config file, and a config file is something a
// team drop-in or a repository can bring. The header drew it as it was, so a
// name carrying an escape sequence coloured the rest of the line, one carrying
// an OSC 8 made the word beside it a link to an address the file chose, and a
// raw U+009B (the one-byte CSI, which xterm-family terminals act on) or a
// right-to-left override reached the terminal untouched. A name is shown the
// way every other listed name is: as it is when it reads as itself, and
// otherwise quoted with each character a person would not see written out.
func TestAViewNameDrawnInTheHeaderNeverActsOnTheTerminal(t *testing.T) {
	hostile := map[string]string{
		"sgr":  "vA\x1b[31;1mRED",
		"osc8": "vC\x1b]8;;http://evil.example/\x1b\\click\x1b]8;;\x1b\\",
		"c1":   "vH\u009b31mred",
		"rlo":  "vF\u202eabc",
		"bel":  "vB\x07ell",
		"nl":   "vN\nforged",
	}
	for name, view := range hostile {
		t.Run(name, func(t *testing.T) {
			m := New(registry.New(), config.Dashboard{}, nil)
			sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			m = sized.(Model)
			m.view = view
			header := strings.SplitN(m.dashboardView(), "\n", 2)[0]
			for _, r := range []rune{0x07, 0x9b, 0x202e, '\n'} {
				if strings.ContainsRune(header, r) {
					t.Errorf("the header carries %U from the view name: %q", r, header)
				}
			}
			// The header is styled, so an escape of its own is expected; what
			// must not be there is one the name brought.
			for _, brought := range []string{"\x1b[31;1m", "\x1b]8;", "\x1b\\"} {
				if strings.Contains(header, brought) {
					t.Errorf("the header passes the view name's own sequence (%q) through: %q", brought, header)
				}
			}
			if !strings.Contains(header, "view ") {
				t.Errorf("the header lost the view line: %q", header)
			}
			if name == "nl" && !strings.Contains(header, "forged") {
				t.Errorf("a newline in the name started a second line instead of being written out: %q", header)
			}
		})
	}
}

// An ordinary name is drawn as it is.
func TestAnOrdinaryViewNameIsDrawnAsItIs(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = sized.(Model)
	m.view = "ops-eu"
	if header := strings.SplitN(m.dashboardView(), "\n", 2)[0]; !strings.Contains(plain(header), "view ops-eu") {
		t.Errorf("an ordinary view name was changed: %q", header)
	}
}
