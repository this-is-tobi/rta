package app

import (
	"strings"
	"testing"
)

// Bare `rta` on a terminal that says TERM=dumb prints help, as it does in a
// pipe, instead of opening a TUI that needs to move the cursor. Without the
// check this test would open the real program and hang, so a regression shows
// as a timeout rather than a message.
func TestBareRtaOnADumbTerminalPrintsHelpNotTheTUI(t *testing.T) {
	onATerminal(t)
	t.Setenv("TERM", "dumb")
	out, errOut, err := run(t, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "single extendable binary") {
		t.Errorf("no help was printed:\n%s", out)
	}
	if !strings.Contains(errOut, "TERM=dumb") {
		t.Errorf("nothing said why help came instead of the TUI: %q", errOut)
	}
}
