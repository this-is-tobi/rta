package mcp

import (
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/consent"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/lockdown"
	"github.com/this-is-tobi/rule-them-all/internal/operator"
)

// A locked agent is refused on every tool — the ungated read tier
// included, which is the surface revoking grants never touched — the lock
// lands live on a running server, and lifting it restores the ordinary
// gates. The pin re-reads per call, so no restart appears anywhere in this
// test.
func TestALockedAgentIsRefusedEverythingUntilLifted(t *testing.T) {
	s := connect(t, Options{Agent: "claude"})

	if res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"}); res.IsError {
		t.Fatalf("the read tier is closed before any lock exists: %s", res.Content[0].(*sdk.TextContent).Text)
	}
	l, verr := lockdown.Build("agent", "claude", "runaway loop", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"})
	if !res.IsError {
		t.Fatal("a locked agent's read went through")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.lock.frozen") || !strings.Contains(text, "runaway loop") {
		t.Fatalf("the refusal neither names the lock nor carries its note: %s", text)
	}
	if _, verr := lockdown.Remove(lockdown.KindAgent, "claude"); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"}); res.IsError {
		t.Fatalf("the lifted lock still refuses: %s", res.Content[0].(*sdk.TextContent).Text)
	}
}

// A lock is the "stop asking me" control: with consent on, a locked
// agent's call is refused outright, never parked as a question for the
// operator who just locked it.
func TestALockedAgentsCallIsNeverParked(t *testing.T) {
	s := connect(t, Options{
		AllowWrite:  []string{"demo"},
		Consent:     true,
		ConsentWait: 5 * time.Second,
		Agent:       "claude",
	})
	l, _ := lockdown.Build("agent", "claude", "", "", "terminal")
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "db-password"})
	if !res.IsError || !strings.Contains(res.Content[0].(*sdk.TextContent).Text, "core.lock.frozen") {
		t.Fatalf("a locked agent's call was not refused as locked: %+v", res)
	}
	pending, err := consent.Pending()
	if err != nil || len(pending) != 0 {
		t.Fatalf("the refused call parked anyway: %+v, %v", pending, err)
	}
}

// The lock verbs over the operator channel, end to end: place a lock on an
// agent from an enrolled operator's key, see it listed, lift it — and the
// stored row is attributed to the operator who placed it, not to the
// machine it landed on.
func TestLockVerbsFreezeAndLiftOverTheChannel(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	status, body := call(operator.VerbLockAdd, []byte(`{"kind":"agent","name":"claude","note":"incident 42"}`))
	if status != http.StatusOK {
		t.Fatalf("lock.add: %d — %s", status, body)
	}
	locks, verr := lockdown.Load()
	if verr != nil || len(locks) != 1 || locks[0].By != grant.FromOperatorPrefix+"tobi" {
		t.Fatalf("the placed lock is not attributed to the operator: %+v, %v", locks, verr)
	}

	status, body = call(operator.VerbLockList, nil)
	if status != http.StatusOK || !strings.Contains(string(body), "incident 42") {
		t.Fatalf("lock.list: %d — %s", status, body)
	}

	if _, body := call(operator.VerbLockAdd, []byte(`{"kind":"agnet","name":"x"}`)); errCode(t, body) != "core.lock.kind" {
		t.Fatalf("a typo'd kind was accepted over the wire: %s", body)
	}

	status, body = call(operator.VerbLockRm, []byte(`{"kind":"agent","name":"claude"}`))
	if status != http.StatusOK || !strings.Contains(string(body), `"removed":true`) {
		t.Fatalf("lock.rm: %d — %s", status, body)
	}
	if locks, _ := lockdown.Load(); len(locks) != 0 {
		t.Fatalf("the lifted lock survives: %+v", locks)
	}
}

// A locked operator key gets no verb at all — the check sits past the
// signature and before the role gate, so even lock.rm is refused: first
// lock wins, and recovery for a fully locked-out roster is a person at the
// machine's own terminal, which is where lockdown.Remove runs directly.
func TestALockedOperatorKeyGetsNoVerb(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	l, _ := lockdown.Build("operator", "tobi", "key reported stolen", "", "terminal")
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	for _, verb := range []string{operator.VerbStatus, operator.VerbLockRm} {
		status, body := call(verb, []byte(`{"kind":"operator","name":"tobi"}`))
		if errCode(t, body) != "core.lock.operator" {
			t.Fatalf("%s from a locked key: %s", verb, body)
		}
		if status != http.StatusForbidden {
			t.Fatalf("%s from a locked key: HTTP %d, want 403", verb, status)
		}
		if !strings.Contains(string(body), "key reported stolen") {
			t.Fatalf("the refusal does not carry the note written for the locked party: %s", body)
		}
	}

	// The terminal is the recovery path.
	if _, verr := lockdown.Remove(lockdown.KindOperator, "tobi"); verr != nil {
		t.Fatal(verr)
	}
	if status, body := call(operator.VerbStatus, nil); status != http.StatusOK {
		t.Fatalf("the lifted operator lock still refuses: %d — %s", status, body)
	}
}

// lockdown's credential-name bound must track this package's
// maxCredentialName: the bridge matches locks against credentialName's
// bounded output, so any name the bridge can present must be one Add
// accepts — both constants are visible here, which makes this the one
// place the agreement can be pinned.
func TestACredentialLockCanNameEverythingTheBridgePresents(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	longest := strings.Repeat("a", maxCredentialName)
	if _, verr := lockdown.Build("credential", longest, "", "", "terminal"); verr != nil {
		t.Fatalf("a name at the bridge's own bound refused: %v", verr)
	}
	if _, verr := lockdown.Build("credential", longest+"a", "", "", "terminal"); verr == nil {
		t.Fatal("a name past the bridge's bound enrolled — it could never match")
	}
}

// A lock placed while a call sits parked must poison that call: the top
// check ran before it parked, so the pin is consulted again on the way out
// of consent — an approval races a lock, the lock wins.
func TestALockPlacedWhileParkedPoisonsTheApproval(t *testing.T) {
	s := connect(t, Options{
		AllowWrite:  []string{"demo"},
		Consent:     true,
		ConsentWait: 20 * time.Second,
		Agent:       "claude",
	})
	go func() {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			pending, err := consent.Pending()
			if err == nil && len(pending) > 0 {
				l, _ := lockdown.Build("agent", "claude", "locked mid-park", "", "terminal")
				if verr := lockdown.Add(l); verr != nil {
					t.Errorf("Add: %v", verr)
				}
				if err := consent.Decide(pending[0].ID, true, "test"); err != nil {
					t.Errorf("Decide: %v", err)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("no request was ever parked")
	}()
	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "db-password"})
	if !res.IsError {
		t.Fatal("an approval that raced a lock ran the call")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "core.lock.frozen") {
		t.Fatalf("the poisoned approval is not refused as locked: %s", text)
	}
}
