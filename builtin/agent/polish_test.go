package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// An empty queue is a sentence, the way an empty lock list is — not a
// bordered table with a header row and nothing under it.
func TestAnEmptyQueueIsASentence(t *testing.T) {
	isolate(t)
	v, err := run(t, "agent.pending", nil)
	if err != nil {
		t.Fatal(err)
	}
	text, ok := v.(view.Text)
	if !ok || !strings.Contains(text.Body, "nothing is waiting") {
		t.Fatalf("empty queue = %+v, want a sentence", v)
	}
}

func lock(t *testing.T, name string) {
	t.Helper()
	l, verr := lockdown.Build("agent", name, "incident", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
}

func pairValue(v view.View, key string) string {
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

// A lock was visible on `lock list` and nowhere else; the dashboard tile
// now names what is frozen, and says "nothing" when nothing is.
func TestTheOverviewNamesWhatIsLocked(t *testing.T) {
	isolate(t)
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "locked"); got != "nothing" {
		t.Fatalf("locked = %q on a clean machine", got)
	}
	lock(t, "claude")
	v, err = run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "locked"); !strings.HasPrefix(got, "claude") {
		t.Fatalf("locked = %q, want the frozen agent named", got)
	}
}

// The bridge refuses a frozen agent before any other gate, so an answer
// releases nothing while the lock stands. The operator's screen says so
// rather than reporting "allowed" for a call that is about to be refused.
func TestAllowingALockedAgentsCallSaysItIsRefusedAnyway(t *testing.T) {
	isolate(t)
	p, err := consent.Ask(consent.Call{
		Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"}, Agent: "claude",
		Args: map[string]any{"key": "db-password"}, Why: "no active grant",
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	lock(t, "claude")
	v, err := run(t, "agent.allow", map[string]any{"id": p.Request.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "but"); !strings.Contains(got, "claude is locked") {
		t.Fatalf("answer = %+v, want the lock named", v)
	}
}
