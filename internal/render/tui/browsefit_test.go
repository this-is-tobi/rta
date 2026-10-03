package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// The catalogue's list is sized to the window less its own header and footer,
// and that was done once, when the window's size arrived, with whatever the
// footer was then. A message in the footer — `+` on the dashboard says "pick a
// capability" — makes it two lines, so the frame was one line taller than the
// terminal and the renderer cut the bottom off: the very line that was just
// put there. Seen in a real terminal; the model's own frame, read as text, had
// the line all along. The size follows the footer now, after every message.
func TestTheCatalogueFitsTheTerminalWhateverTheFooterSays(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}} {
		m, _ := realModel(t, size[0], size[1])
		m = press(t, m, "+")
		content := plain(m.View().Content)
		if h := lipgloss.Height(content); h > size[1] {
			t.Errorf("%dx%d: the catalogue is %d lines after +, taller than the terminal:\n%s", size[0], size[1], h, content)
		}
		if !strings.Contains(content, "pick a capability") {
			t.Errorf("%dx%d: the hint + put in the footer is not on the screen:\n%s", size[0], size[1], content)
		}
		// And back to the room it had once the message is gone.
		m = press(t, m, "down")
		content = plain(m.View().Content)
		if h := lipgloss.Height(content); h > size[1] {
			t.Errorf("%dx%d: the catalogue is %d lines after a key, taller than the terminal", size[0], size[1], h)
		}
	}
}
