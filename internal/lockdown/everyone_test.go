package lockdown

import (
	"strings"
	"testing"
	"time"
)

// The stop an incident needs is the one that does not wait to learn a name:
// an agent that connects after the operator started typing `lock add` per name
// is frozen when it arrives.
func TestALockOnEveryAgentFreezesWhicheverAgentAsks(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", Everyone, "paused while we read the refusals", "")
	pin := NewPin()
	for _, agent := range []string{"claude", "cursor", "an-agent-that-has-not-connected-yet", ""} {
		l, _ := pin.Check(agent, "")
		if l == nil {
			t.Errorf("agent %q was let through the lock on every agent", agent)
			continue
		}
		if l.Note != "paused while we read the refusals" {
			t.Errorf("agent %q read %q, want the note the operator wrote", agent, l.Note)
		}
	}
	if l, _ := pin.Frozen(KindCredential, "ci-token"); l != nil {
		t.Error("a lock on every agent froze a credential")
	}
	if l, _ := pin.Frozen(KindOperator, "alice"); l != nil {
		t.Error("a lock on every agent froze an operator, who is the one person who can lift it from afar")
	}
}

// A lock on the name itself is the one a note was written for.
func TestALockOnTheNameBeatsTheOneOnEveryAgent(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", Everyone, "everyone", "")
	mustAdd(t, "agent", "claude", "just you", "")
	l, _ := NewPin().Check("claude", "")
	if l == nil || l.Note != "just you" {
		t.Fatalf("claude read %+v, want the note written for claude", l)
	}
	if l, _ := NewPin().Check("cursor", ""); l == nil || l.Note != "everyone" {
		t.Fatalf("cursor read %+v, want the note written for everyone", l)
	}
}

// The wildcard is a spelling of the agent kind alone. A credential is whatever
// a verifier proved, and one that happens to be spelled "*" is that credential
// and no other; an operator label is held to the grammar of an agent name.
func TestOnlyAnAgentLockCanBeAWildcard(t *testing.T) {
	fresh(t)
	mustAdd(t, "credential", "*", "", "")
	if l, _ := NewPin().Frozen(KindCredential, "someone-else"); l != nil {
		t.Errorf("a credential lock named * froze another credential: %+v", l)
	}
	if l, _ := NewPin().Frozen(KindCredential, "*"); l == nil {
		t.Error("a credential lock named * did not freeze the credential of that name")
	}
	if _, verr := Build("operator", "*", "", "", "terminal"); verr == nil {
		t.Error("an operator label of * was accepted, and no operator is named that")
	}
	if l, _ := NewPin().Check("claude", ""); l != nil {
		t.Errorf("a credential lock froze an agent: %+v", l)
	}
}

// Lifted as it was placed, and nothing else with it.
func TestLiftingTheLockOnEveryAgentLeavesTheOnesOnSingleAgents(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", Everyone, "", "")
	mustAdd(t, "agent", "claude", "", "")
	if removed, verr := Remove(KindAgent, Everyone); verr != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, verr)
	}
	pin := NewPin()
	if l, _ := pin.Check("claude", ""); l == nil {
		t.Error("claude was unfrozen with everybody")
	}
	if l, _ := pin.Check("cursor", ""); l != nil {
		t.Error("cursor is still frozen after the lock on every agent was lifted")
	}
}

func TestATTLOnTheLockOnEveryAgentLiftsIt(t *testing.T) {
	fresh(t)
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	stopClock(t, at)
	mustAdd(t, "agent", Everyone, "", "30m")
	stopClock(t, at.Add(31*time.Minute))
	if l, _ := NewPin().Check("claude", ""); l != nil {
		t.Errorf("the lock on every agent outlived its window: %+v", l)
	}
}

func TestLabelSaysAWildcardInWords(t *testing.T) {
	for _, c := range []struct {
		l    Lock
		want string
	}{
		{Lock{Kind: KindAgent, Name: Everyone}, "every agent"},
		{Lock{Kind: KindAgent, Name: "claude"}, "claude"},
		{Lock{Kind: KindCredential, Name: "ci-token"}, "ci-token (credential)"},
		{Lock{Kind: KindCredential, Name: "*"}, "* (credential)"},
	} {
		if got := c.l.Label(); got != c.want {
			t.Errorf("%+v is %q, want %q", c.l, got, c.want)
		}
	}
}

// A day is the first unit anybody asking for "the weekend" or "tomorrow"
// reaches for, and the one time.ParseDuration has none for.
func TestALockWindowCanBeInDays(t *testing.T) {
	fresh(t)
	at := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	stopClock(t, at)
	l, verr := Build("agent", "claude", "", "2d", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if want := at.Add(48 * time.Hour); !l.Expires.Equal(want) {
		t.Errorf("2d lifts at %v, want %v", l.Expires, want)
	}
	_, verr = Build("agent", "claude", "", "soon", "terminal")
	if verr == nil || !strings.Contains(verr.Message, "30m, 2h, 1d") {
		t.Errorf("a window that is none says %v, want the spellings it takes", verr)
	}
}
