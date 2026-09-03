package mcp

import (
	"net/http"
	"testing"

	"github.com/this-is-tobi/rule-them-all/internal/agentlog"
	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/lockdown"
	"github.com/this-is-tobi/rule-them-all/internal/operator"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The operator channel's mutations land in the agent ledger — the same
// sealed file the bridge writes — attributed to the enrolled key that
// signed them. These tests pin the recording rule from every side: what
// mutates is recorded whatever its outcome, what reads is not, and the
// row is durable before the caller sees the answer (recording runs before
// writeJSON, so a row asserted right after postEnvelope returns is not a
// race).

func ledgerEntries(t *testing.T) []agentlog.Entry {
	t.Helper()
	entries, err := agentlog.Read(0)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestAnOperatorMutationLandsInTheLedger(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	if status, body := call(operator.VerbLockAdd,
		[]byte(`{"kind":"agent","name":"claude","note":"incident 42"}`)); status != http.StatusOK {
		t.Fatalf("lock.add: %d — %s", status, body)
	}
	if status, body := call(operator.VerbLockRm,
		[]byte(`{"kind":"agent","name":"claude"}`)); status != http.StatusOK {
		t.Fatalf("lock.rm: %d — %s", status, body)
	}

	entries := ledgerEntries(t)
	if len(entries) != 2 {
		t.Fatalf("two mutations wrote %d entries: %+v", len(entries), entries)
	}
	add := entries[0]
	if add.Cap != "operator.lock.add" || add.Agent != "demo-agent" ||
		add.Credential != grant.FromOperatorPrefix+"tobi" {
		t.Fatalf("the add row misnames who did what where: %+v", add)
	}
	if add.Outcome != agentlog.Ran || add.Auth != agentlog.Operator || add.Reason != "" {
		t.Fatalf("an allowed mutation's outcome: %+v", add)
	}
	if add.Args["kind"] != "agent" || add.Args["name"] != "claude" || add.Args["note"] != "incident 42" {
		t.Fatalf("the add row's args: %+v", add.Args)
	}
	if rm := entries[1]; rm.Cap != "operator.lock.rm" || rm.Outcome != agentlog.Ran {
		t.Fatalf("the rm row: %+v", rm)
	}
}

// Reads never reach the record — a watching dashboard polls them — and a
// dry-run revoke is a read in a mutation verb's clothes. The refused read
// (consent.list on a server without --consent) pins that the rule is
// verb-shaped, not outcome-shaped: an unrecorded verb stays unrecorded
// when it fails, too.
func TestOperatorReadsAndPreviewsStayOffTheRecord(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{
		Roster: roster,
		Revoke: func(spec operator.RevokeSpec, write bool) (operator.RevokeOutcome, *view.Error) {
			if write {
				t.Fatal("a dry-run revoke reached the store as a write")
			}
			return operator.RevokeOutcome{}, nil
		},
	})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	call(operator.VerbStatus, nil)
	call(operator.VerbGrantList, nil)
	call(operator.VerbLockList, nil)
	call(operator.VerbConsentList, nil)
	call(operator.VerbGrantRevoke, []byte(`{"target":"demo.item.reveal","dryRun":true}`))

	if entries := ledgerEntries(t); len(entries) != 0 {
		t.Fatalf("reads wrote %d ledger entries: %+v", len(entries), entries)
	}
}

func TestARefusedOperatorMutationIsARecordedRefusal(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolledAs(t, "watcher", " role=read")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr

	status, body := postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr),
		operator.VerbLockAdd, []byte(`{"kind":"agent","name":"claude"}`)))
	if status != http.StatusForbidden {
		t.Fatalf("a read key's lock.add: %d — %s", status, body)
	}

	entries := ledgerEntries(t)
	if len(entries) != 1 {
		t.Fatalf("one refused mutation wrote %d entries: %+v", len(entries), entries)
	}
	e := entries[0]
	if e.Outcome != agentlog.Refused || e.Auth != agentlog.Blocked ||
		e.Code != "core.operator.role" {
		t.Fatalf("the refusal row: %+v", e)
	}
	if e.Credential != grant.FromOperatorPrefix+"watcher" || e.Args["name"] != "claude" {
		t.Fatalf("the refusal row still names who asked for what: %+v", e)
	}
}

func TestALockedOperatorKeysAttemptIsRecorded(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr

	l, _ := lockdown.Build("operator", "tobi", "key reported stolen", "", "terminal")
	if verr := lockdown.Add(l); verr != nil {
		t.Fatal(verr)
	}
	status, body := postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr),
		operator.VerbLockRm, []byte(`{"kind":"operator","name":"tobi"}`)))
	if status != http.StatusForbidden {
		t.Fatalf("the locked key's lock.rm: %d — %s", status, body)
	}

	entries := ledgerEntries(t)
	if len(entries) != 1 {
		t.Fatalf("the locked key's attempt wrote %d entries: %+v", len(entries), entries)
	}
	if e := entries[0]; e.Outcome != agentlog.Refused ||
		e.Code != "core.lock.operator" {
		t.Fatalf("the locked-key row: %+v", e)
	}
}

// The consent.answer row is the ledger's other half of an approval: the
// agent's own parked call records auth=approved, and this row records who
// answered — the pairing a later dual-authorization check would read.
// The revoke rows pin the Refused/Failed split riding on statusFor: an
// unnamed error code is a 500 and a Failed row with the operator still
// authorized, not a refusal.
func TestAnswersAndFailuresRecordWhoWasActing(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{
		Roster:  roster,
		Consent: true,
		Answer: func(spec operator.AnswerSpec, label string) (operator.AnswerOutcome, *view.Error) {
			if label != "tobi" || spec.ID != "req-1" || !spec.Allow {
				return operator.AnswerOutcome{}, view.Errorf("core.consent.unknown", "no such request")
			}
			return operator.AnswerOutcome{Cap: "demo.item.reveal"}, nil
		},
		Revoke: func(spec operator.RevokeSpec, write bool) (operator.RevokeOutcome, *view.Error) {
			if spec.Target == "" && !spec.All {
				return operator.RevokeOutcome{}, view.Errorf("grant.notarget", "name a capability, or pass --all")
			}
			return operator.RevokeOutcome{}, view.Errorf("core.grant.store", "the store is unreadable")
		},
	})
	at := "http://" + addr
	call := func(verb string, payload []byte) (int, []byte) {
		return postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr), verb, payload))
	}

	if status, body := call(operator.VerbConsentAnswer,
		[]byte(`{"id":"req-1","digest":"d","allow":true}`)); status != http.StatusOK {
		t.Fatalf("consent.answer: %d — %s", status, body)
	}
	if status, _ := call(operator.VerbGrantRevoke,
		[]byte(`{"target":"demo.item.reveal"}`)); status != http.StatusInternalServerError {
		t.Fatalf("a store failure did not answer 500: %d", status)
	}
	// A wired package's refusal — a code statusFor names from outside the
	// channel's own core.operator vocabulary — records Refused, never
	// Failed: the row must be findable by the operator grepping refusals.
	if status, _ := call(operator.VerbGrantRevoke, []byte(`{}`)); status != http.StatusBadRequest {
		t.Fatalf("a selector-less revoke did not answer 400: %d", status)
	}

	entries := ledgerEntries(t)
	if len(entries) != 3 {
		t.Fatalf("wrote %d entries: %+v", len(entries), entries)
	}
	ans := entries[0]
	if ans.Cap != "operator.consent.answer" || ans.Outcome != agentlog.Ran ||
		ans.Args["id"] != "req-1" || ans.Args["allow"] != true {
		t.Fatalf("the answer row: %+v", ans)
	}
	failed := entries[1]
	if failed.Cap != "operator.grant.revoke" || failed.Outcome != agentlog.Failed ||
		failed.Auth != agentlog.Operator || failed.Code != "core.grant.store" {
		t.Fatalf("the failed row: %+v", failed)
	}
	if failed.Args["target"] != "demo.item.reveal" {
		t.Fatalf("the failed row's args: %+v", failed.Args)
	}
	if refused := entries[2]; refused.Outcome != agentlog.Refused || refused.Auth != agentlog.Blocked ||
		refused.Code != "grant.notarget" {
		t.Fatalf("a wired refusal's row: %+v", refused)
	}
}

// A mutation whose payload does not decode into its spec still records
// the attempt — the enrolled key asked for something, dispatch refused
// it, and the row carries that refusal with no arguments rather than
// nothing at all. (Well-formed JSON of the wrong shape, because the
// envelope embeds its payload verbatim: syntactically broken JSON cannot
// ride an envelope at all.)
func TestAnUndecodableMutationStillRecordsTheAttempt(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "tobi")
	addr := startOperatorWith(t, OperatorConfig{Roster: roster})
	at := "http://" + addr

	status, body := postEnvelope(t, addr, signer.Sign(at, fetchChallenge(t, addr),
		operator.VerbLockAdd, []byte(`{"kind":123}`)))
	if status != http.StatusBadRequest {
		t.Fatalf("garbage payload: %d — %s", status, body)
	}
	entries := ledgerEntries(t)
	if len(entries) != 1 {
		t.Fatalf("wrote %d entries: %+v", len(entries), entries)
	}
	if e := entries[0]; e.Outcome != agentlog.Refused || len(e.Args) != 0 ||
		e.Code != "core.operator.payload" {
		t.Fatalf("the undecodable row: %+v", e)
	}
}

// Every verb the wire declares is classified: a new mutation constant
// added without a mutationArgs case would ship unrecorded, and this walk
// is what fails instead of a reviewer noticing.
func TestEveryVerbHasARecordingDecision(t *testing.T) {
	mutations := map[string]bool{
		operator.VerbGrantRevoke:   true,
		operator.VerbGrantIssue:    true,
		operator.VerbConsentAnswer: true,
		operator.VerbLockAdd:       true,
		operator.VerbLockRm:        true,
	}
	reads := []string{
		operator.VerbStatus, operator.VerbGrantList, operator.VerbGrantPrepare,
		operator.VerbConsentList, operator.VerbLockList,
	}
	for verb := range mutations {
		if _, ok := mutationArgs(operator.Envelope{Verb: verb, Payload: []byte(`{}`)}); !ok {
			t.Errorf("%s is a mutation and is not recorded", verb)
		}
	}
	for _, verb := range reads {
		if _, ok := mutationArgs(operator.Envelope{Verb: verb, Payload: []byte(`{}`)}); ok {
			t.Errorf("%s is a read and is recorded", verb)
		}
	}
	// The two lists together are the wire's whole vocabulary, so a verb
	// added there lands in exactly one of them or this count breaks.
	if want := len(operator.Verbs()); len(mutations)+len(reads) != want {
		t.Errorf("the wire declares %d verbs and this test classifies %d — "+
			"decide whether the new verb is recorded and add it to a list", want, len(mutations)+len(reads))
	}
}
