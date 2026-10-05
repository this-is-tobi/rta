package tui

import (
	"os"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/internal/session"
)

func formFields(m Model) map[string]bool {
	out := map[string]bool{}
	if m.form != nil {
		for _, f := range m.form.fields {
			out[f.Name] = true
		}
	}
	return out
}

// A standing grant or a whole role is the other answer, and it is one key
// away: a capital, which is the form the lower case used to open.
func TestACapitalAOpensTheFormForTheSameCall(t *testing.T) {
	id := parkCall(t, "note.rm", "2", "would remove note 2")
	form := press(t, screenOf(t, "agent.pending", nil), "A")
	if form.mode != modeForm || form.armed != nil {
		t.Fatalf("A left %v, armed %v, want the form", form.mode, form.armed)
	}
	fields := formFields(form)
	if !fields["ttl"] || !fields["role"] {
		t.Errorf("the form asks %v, want ttl and role", fields)
	}
	if fields["passphrase"] {
		t.Error("the guard is off and the form still asks for its passphrase")
	}
	if fields["id"] || form.form.base["id"] != id {
		t.Errorf("the form should hold the call as given, not ask for it: base %v fields %v", form.form.base, fields)
	}
	armed := press(t, screenOf(t, "agent.pending", nil), "a")
	if viaA := press(t, armed, "A"); viaA.mode != modeForm || !formFields(viaA)["ttl"] {
		t.Errorf("A while armed left %v, want the same form", viaA.mode)
	}
}

// The passphrase is the guard's, and a machine without one has nothing to type.
func TestThePassphraseIsAskedOnlyWhenTheGuardIsOn(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	if _, verr := guard.Enable("a passphrase for the test"); verr != nil {
		t.Fatalf("turning the guard on: %v", verr)
	}
	form := press(t, screenOf(t, "agent.pending", nil), "A")
	if !formFields(form)["passphrase"] {
		t.Errorf("the guard is on and the form does not ask for its passphrase: %v", formFields(form))
	}
}

// An answer aimed at another server is signed there, and that is a form.
func TestAnAnswerAimedAtAServerOpensTheFormNotTheShortcut(t *testing.T) {
	_ = parkCall(t, "note.rm", "2", "would remove note 2")
	queue := screenOf(t, "agent.pending", nil)
	queue.lastValues = map[string]any{"server": "lab"}
	got := press(t, queue, "a")
	if got.armed != nil {
		t.Error("the shortcut answered a call that is on another machine's queue")
	}
}

// `L` from a screen that has no row to take the name from: the agent that is
// connected, when there is exactly one.
func TestTheLockFormOpensOnTheConnectedAgent(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	start := func(id, agent string) {
		if err := session.Start(session.Record{ID: id, Agent: agent, Since: time.Now(), PID: os.Getpid()}); err != nil {
			t.Fatal(err)
		}
	}
	m, _ := realModel(t, 120, 40)
	c := mustCap(t, m.reg, lockCapability)
	if got := m.suggested(c); got != nil {
		t.Errorf("suggested %v with nobody connected", got)
	}
	start("a1", "claude")
	start("a2", "claude")
	got := m.suggested(c)
	if got["name"] != "claude" {
		t.Fatalf("suggested %v, want the agent both servers run as", got)
	}
	opened, _ := m.openAction(c)
	form := opened.(Model)
	if form.mode != modeForm || form.form.seed["name"] != "claude" {
		t.Errorf("the lock form opened %v seeded %v, want the connected agent in the name box", form.mode, form.form.seed)
	}
	start("a3", "cursor")
	if got := m.suggested(c); got != nil {
		t.Errorf("suggested %v with two agents connected: a lock on the wrong one looks like one on the right one", got)
	}
	if got := m.suggested(mustCap(t, m.reg, "lock.rm")); got != nil {
		t.Errorf("suggested %v for lock.rm", got)
	}
}
