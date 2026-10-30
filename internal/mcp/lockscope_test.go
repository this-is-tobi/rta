package mcp

import (
	"net/http"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/internal/operator"
)

// The lock file is every server's of this account, and a roster is one
// server's trust decision. An operator enrolled only on a staging server
// (--as staging) lifted the lock the terminal had placed on the prod server's
// agent and on one of prod's operators, and read every lock on the machine.
// The lock verbs act on what the server they reach owns: its own agent, its
// own operators, and the machine-wide locks, which are lifted only where they
// were placed.
func TestAnOperatorOfOneServerCannotLiftAnotherServersLocks(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "contractor")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster, Agent: "staging"})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}
	place := func(kind, name string) {
		t.Helper()
		lk, verr := lockdown.Build(kind, name, "incident", "", "terminal")
		if verr != nil {
			t.Fatal(verr)
		}
		if verr := lockdown.Add(lk); verr != nil {
			t.Fatal(verr)
		}
	}
	place("agent", "prod")
	place("operator", "prod-oncall")

	if _, body := call(operator.VerbLockList, nil); strings.Contains(string(body), `"prod`) {
		t.Errorf("staging's operator reads the prod server's locks: %s", body)
	}
	for _, p := range []string{`{"kind":"agent","name":"prod"}`, `{"kind":"operator","name":"prod-oncall"}`} {
		if _, body := call(operator.VerbLockRm, []byte(p)); errCode(t, body) != "core.operator.lock.scope" {
			t.Errorf("lifting %s: %s, want core.operator.lock.scope", p, body)
		}
		if _, body := call(operator.VerbLockAdd, []byte(p)); errCode(t, body) != "core.operator.lock.scope" {
			t.Errorf("placing %s: %s, want core.operator.lock.scope", p, body)
		}
	}
	if locks, _ := lockdown.Load(); len(locks) != 2 {
		t.Fatalf("locks left after staging's operator acted: %+v", locks)
	}

	// Its own agent and its own operators are its to lock and lift, whoever
	// placed the lock.
	place("agent", "staging")
	if status, body := call(operator.VerbLockRm, []byte(`{"kind":"agent","name":"staging"}`)); status != http.StatusOK ||
		!strings.Contains(string(body), `"removed":true`) {
		t.Errorf("staging's operator could not lift the terminal's lock on staging: %d %s", status, body)
	}

	// A lock on every agent may be placed through any server and is lifted
	// through the one that placed it — not one the terminal placed.
	if status, body := call(operator.VerbLockAdd, []byte(`{"kind":"agent","name":"*"}`)); status != http.StatusOK {
		t.Fatalf("placing a lock on every agent: %d %s", status, body)
	}
	if status, body := call(operator.VerbLockRm, []byte(`{"kind":"agent","name":"*"}`)); status != http.StatusOK ||
		!strings.Contains(string(body), `"removed":true`) {
		t.Errorf("staging could not lift the lock on every agent it placed: %d %s", status, body)
	}
	place("agent", lockdown.Everyone)
	if _, body := call(operator.VerbLockList, nil); !strings.Contains(string(body), `"name":"*"`) {
		t.Errorf("a lock on every agent, which holds here too, is not listed: %s", body)
	}
	if _, body := call(operator.VerbLockRm, []byte(`{"kind":"agent","name":"*"}`)); errCode(t, body) != "core.operator.lock.scope" {
		t.Errorf("staging lifted the terminal's lock on every agent: %s", body)
	}
}
