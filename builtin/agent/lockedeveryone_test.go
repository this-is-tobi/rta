package agent

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

func freezeAgent(t *testing.T, name string) {
	t.Helper()
	l, verr := lockdown.Build("agent", name, "", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
}

func pairMap(t *testing.T, v view.View) map[string]string {
	t.Helper()
	kv, ok := v.(view.KeyValue)
	if !ok {
		t.Fatalf("got %s, want pairs", view.TypeOf(v))
	}
	out := map[string]string{}
	for _, p := range kv.Pairs {
		out[p.Key] = p.Value
	}
	return out
}

// A bare `*` in a line about what is frozen reads as a glob nobody typed.
func TestTheOverviewNamesTheLockOnEveryAgentInWords(t *testing.T) {
	isolate(t)
	freezeAgent(t, lockdown.Everyone)
	freezeAgent(t, "claude")
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pairMap(t, v)["locked"], "every agent, claude — `rta lock list` says why"; got != want {
		t.Errorf("locked = %q, want %q", got, want)
	}
}

// The bridge refuses a frozen agent before any other gate, and the answer that
// releases nothing says which lock to lift: the one on every agent is lifted as
// it was placed, not by a name nobody gave it.
func TestAllowingForAFrozenAgentNamesTheLockThatHoldsIt(t *testing.T) {
	isolate(t)
	freezeAgent(t, lockdown.Everyone)
	r := parkedRemoval(t)
	v, err := run(t, "agent.allow", map[string]any{"id": r.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := "every agent is locked, so the call is refused anyway until `rta lock rm --all`"
	if got := pairMap(t, v)["but"]; got != want {
		t.Errorf("but = %q, want %q", got, want)
	}

	isolate(t)
	freezeAgent(t, "claude")
	r = parkedRemoval(t)
	v, err = run(t, "agent.allow", map[string]any{"id": r.ID})
	if err != nil {
		t.Fatal(err)
	}
	want = "agent claude is locked, so the call is refused anyway until `rta lock rm claude`"
	if got := pairMap(t, v)["but"]; got != want {
		t.Errorf("but = %q, want %q", got, want)
	}
}
