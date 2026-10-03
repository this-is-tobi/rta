package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
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

// Bare `rta` over a config file that does not parse refuses to open the TUI
// and does not say the file's own error a second time: main writes it to
// stderr before the command runs, and the refusal repeated its parse excerpt
// line for line.
func TestBareRtaOverABrokenConfigDoesNotRepeatTheParseError(t *testing.T) {
	onATerminal(t)
	t.Setenv("TERM", "xterm-256color")
	_, _, err := runWith(t, testRegistry(t), "profiles: [unclosed\n")
	if err == nil {
		t.Fatal("the TUI was opened over a config that does not parse")
	}
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "config.invalid" || ve.Hint == "" {
		t.Fatalf("err = %#v, want config.invalid with a hint", err)
	}
	if strings.Contains(ve.Message, "unclosed") || strings.Contains(ve.Message, "parsing") {
		t.Errorf("the refusal repeats the parse error: %q", ve.Message)
	}
}
