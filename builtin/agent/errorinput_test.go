package agent

import (
	"testing"

	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// namesInput holds what capID refused to the one input the refusal is about,
// by the name capID declares it under — "" for a refusal no one box holds the
// fix to.
func namesInput(t *testing.T, capID string, err error, code, input string) {
	t.Helper()
	verr, ok := err.(*view.Error)
	switch {
	case !ok || verr == nil || verr.Code != code:
		t.Errorf("%s: err = %v, want %s", capID, err, code)
	case verr.Input != input:
		t.Errorf("%s: %s names the input %q, want %q", capID, code, verr.Input, input)
	case input != "":
		if _, ok := capability(t, capID).ErrorInput(verr); !ok {
			t.Errorf("%s: %s names %q, which %s does not declare", capID, code, input, capID)
		}
	}
}

// An answer names the request it answers by its id, and every refusal of that
// id names the id box: a form opens again on it with the ids waiting in the
// hint, the remote answer and the operator channel's half of it included. The
// record's since names its own. Nothing waiting at all is the queue's state,
// and names no input.
func TestARefusalOfTheIdOrTheSinceNamesIt(t *testing.T) {
	isolate(t)
	_, err := run(t, "agent.log", map[string]any{"since": "whenever"})
	namesInput(t, "agent.log", err, "agent.log.since", "since")
	for _, capID := range []string{"agent.show", "agent.approve", "agent.deny"} {
		_, err := run(t, capID, map[string]any{"id": "nosuch"})
		namesInput(t, capID, err, "agent.request.unknown", "id")
		_, err = run(t, capID, map[string]any{"server": "lab"})
		namesInput(t, capID, err, "agent.request.unnamed", "id")
	}
	_, err = run(t, "agent.approve", nil)
	namesInput(t, "agent.approve", err, "agent.request.none", "")

	var verr *view.Error
	_, verr = findRemote(operatorid.ConsentList{}, "lab", "nosuch")
	namesInput(t, "agent.show", verr, "agent.request.unknown", "id")
	_, verr = AnswerRemote("claude")(operatorid.AnswerSpec{ID: "nosuch", Allow: true}, "tobi")
	namesInput(t, "agent.approve", verr, "agent.request.unknown", "id")

	was := atTerminal
	t.Cleanup(func() { atTerminal = was })
	atTerminal = func(plugin.Request) bool { return false }
	parkedRemoval(t)
	_, err = allowAt(t, nil, false, false)
	namesInput(t, "agent.approve", err, "agent.request.unnamed", "id")
	park(t, "kv.get", "db-password")
	_, err = run(t, "agent.deny", nil)
	namesInput(t, "agent.deny", err, "agent.request.several", "id")
}
