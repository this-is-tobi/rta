package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/agentlog"
)

// A fence naming a key its plugin does not read leaves a profile every call of
// which is refused: after an upgrade that dropped the key, or a typo that
// loaded because the loader does not know the plugin. The agent hears one
// sentence for every way a profile can be unusable, and the operator is the
// one who has to fix it, so the row must say which key — a row that said only
// "unusable" sent them to a ledger with nothing in it.
func TestABrokenFenceIsNamedInTheRowForTheOperator(t *testing.T) {
	f := newFenceFixture(t, `
profiles:
  broken:
    plugins:
      app:
        only:
          nothere: [x]
`, t.TempDir())
	f.grantFor(t, "broken", 0)

	res := f.call(t, map[string]any{"profile": "broken"})
	if !res.IsError {
		t.Fatalf("a call through a broken fence ran: %s", contentText(t, res))
	}
	if *f.ran != 0 {
		t.Fatal("the handler ran on a connection whose fence could not be read")
	}
	if text := contentText(t, res); strings.Contains(text, "nothere") {
		t.Errorf("the agent is told which key the operator got wrong:\n%s", text)
	}

	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("ledger = %v, %v", entries, err)
	}
	e := entries[0]
	if e.Code != "core.profile.unusable" {
		t.Errorf("the row's code = %q", e.Code)
	}
	if !strings.Contains(e.Note, "nothere") {
		t.Errorf("the row does not say what is wrong with the profile, for the operator: note %q", e.Note)
	}
	if strings.Contains(e.Reason, "nothere") {
		t.Errorf("the reason column carries what the agent was not told: %q", e.Reason)
	}
}
