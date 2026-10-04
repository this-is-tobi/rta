package mcp

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// What an agent is told when a grant it was running on is no longer there:
// that it ended, and how, in the words the record carries too.

func issue(t *testing.T, g grant.Grant) {
	t.Helper()
	if g.Issued.IsZero() {
		g.Issued = time.Now()
	}
	if g.Expires.IsZero() {
		g.Expires = time.Now().Add(time.Hour)
	}
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := grant.Save(append(grants, g)); verr != nil {
		t.Fatal(verr)
	}
}

func refusalText(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("the call was not refused: %+v", res.Content)
	}
	return res.Content[0].(*sdk.TextContent).Text
}

func lastRecord(t *testing.T) agentlog.Entry {
	t.Helper()
	entries, err := agentlog.Read(0)
	if err != nil || len(entries) == 0 {
		t.Fatalf("record: %v, %d entries", err, len(entries))
	}
	return entries[len(entries)-1]
}

func TestAGrantUsedUpIsToldToHaveEnded(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging", MaxUses: 2})

	for range 2 {
		if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}); res.IsError {
			t.Fatalf("a call inside the grant's uses was refused: %+v", res.Content)
		}
	}
	text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}))

	if !strings.Contains(text, "the grant for demo.item.reveal staging ended (2 of 2 uses)") {
		t.Errorf("the agent was not told the grant ran out: %s", text)
	}
	if !strings.Contains(text, "core.grant.required") || !strings.Contains(text, "rta grant allow demo.item.reveal staging") {
		t.Errorf("the refusal lost its code or the command that issues the next grant: %s", text)
	}
	if rec := lastRecord(t); rec.Code != "core.grant.required" ||
		rec.Reason != "the grant for demo.item.reveal staging ended (2 of 2 uses)" {
		t.Errorf("the record does not say what the agent was told: %+v", rec)
	}
}

func TestAOneUseGrantEndsInTheSingular(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", MaxUses: 1})
	callTool(t, s, "demo_item_reveal", map[string]any{"key": "a"})
	if text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "a"})); !strings.Contains(text, "ended (1 of 1 use)") {
		t.Errorf("a one-use grant was not described in the singular: %s", text)
	}
}

// A grant this server never ran a call on is not described, however many uses
// it had: telling the agent would answer, one probe at a time, which records
// the operator once granted to its name.
func TestAGrantThisServerNeverUsedStaysNoActiveGrant(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging", MaxUses: 1})
	spendElsewhere(t, "staging")

	text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}))
	if !strings.Contains(text, "no active grant for demo.item.reveal staging") || strings.Contains(text, "ended") {
		t.Errorf("a grant the agent never used was described to it: %s", text)
	}
}

func spendElsewhere(t *testing.T, key string) {
	t.Helper()
	reg := testRegistry(t)
	c, ok := reg.Capability("demo.item.reveal")
	if !ok {
		t.Fatal("demo.item.reveal is not registered")
	}
	release, verr := grant.Reserve(c, map[string]any{"key": key}, grant.Caller{})
	if verr != nil {
		t.Fatal(verr)
	}
	_ = release
}

func TestAGrantTakenBackIsNotSaidToHaveEnded(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging"})
	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}); res.IsError {
		t.Fatalf("the grant did not cover its own call: %+v", res.Content)
	}
	if verr := grant.Save(nil); verr != nil {
		t.Fatal(verr)
	}
	text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}))
	if strings.Contains(text, "ended") || !strings.Contains(text, "no active grant") {
		t.Errorf("a revoked grant was described as one that ran out: %s", text)
	}
}

func TestAnEndedGrantDoesNotExplainAnotherRecord(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging", MaxUses: 1})
	callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"})

	text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "prod"}))
	if strings.Contains(text, "ended") || !strings.Contains(text, "no active grant for demo.item.reveal prod") {
		t.Errorf("a grant for another record was offered as the reason: %s", text)
	}
}

// The grant that ended is the first one's story. Once a second was issued and
// then taken back, the refusal is not about the first.
func TestTheLatestGrantUsedIsTheOneDescribed(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging", MaxUses: 1})
	callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging"})
	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}); res.IsError {
		t.Fatalf("the second grant did not cover its call: %+v", res.Content)
	}
	if verr := grant.Save(nil); verr != nil {
		t.Fatal(verr)
	}
	text := refusalText(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}))
	if strings.Contains(text, "ended") {
		t.Errorf("the refusal was explained by a grant that was replaced: %s", text)
	}
}

// A call missing several records is not explained by one grant that ended.
func TestACallMissingSeveralRecordsIsNotExplainedByOne(t *testing.T) {
	s := connect(t, Options{})
	issue(t, grant.Grant{Target: "demo.item.export", Scope: "a", MaxUses: 1})
	if res := callTool(t, s, "demo_item_export", map[string]any{"key": []string{"a"}}); res.IsError {
		t.Fatalf("the grant did not cover its own call: %+v", res.Content)
	}
	text := refusalText(t, callTool(t, s, "demo_item_export", map[string]any{"key": []string{"a", "b"}}))
	if strings.Contains(text, "ended") {
		t.Errorf("a refusal for two records was explained by the one that ended: %s", text)
	}
}

// With consent on, the person is asked, and the page they answer from says
// why they are being asked.
func TestTheQuestionSaysTheGrantEnded(t *testing.T) {
	s := connect(t, Options{Consent: true, ConsentWait: 20 * time.Second})
	issue(t, grant.Grant{Target: "demo.item.reveal", Scope: "staging", MaxUses: 1})
	callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"})

	seen := answerWhenAsked(t, false)
	callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"})
	select {
	case req := <-seen:
		if req.Why != "the grant for demo.item.reveal staging ended (1 of 1 use)" {
			t.Errorf("the question does not say why it is being asked: %q", req.Why)
		}
	default:
		t.Fatal("nothing was parked")
	}
}

func TestAnExpiredGrantIsToldToHaveEnded(t *testing.T) {
	e := &endedGrants{}
	g := grant.Grant{
		Target: "demo.item.reveal", Scope: "staging",
		Issued: time.Now().Add(-time.Hour), Expires: time.Now().Add(-time.Minute),
	}
	e.spentOn("a", []grant.Grant{g})
	c := plugin.Capability{ID: "demo.item.reveal"}

	if got, want := e.ended("a", c, "staging", grant.Caller{}, time.Now()), "the grant for demo.item.reveal staging ended (it expired)"; got != want {
		t.Errorf("an expired grant: got %q, want %q", got, want)
	}
	if got := e.ended("a", c, "staging", grant.Caller{}, g.Expires.Add(-time.Second)); got != "" {
		t.Errorf("a grant still inside its time was described as ended: %q", got)
	}
	if got := e.ended("b", c, "staging", grant.Caller{}, time.Now()); got != "" {
		t.Errorf("one caller was told about a grant another used: %q", got)
	}
	if got := e.ended("a", c, "staging", grant.Caller{Agent: "other"}, time.Now()); got != "" {
		t.Errorf("a grant that names no such agent was described to it: %q", got)
	}
}

// The uses it counts are the ones it saw, so a figure it cannot vouch for
// leaves the refusal worded as before.
func TestAGrantIsOnlyEndedByTheUsesThisServerSaw(t *testing.T) {
	e := &endedGrants{}
	c := plugin.Capability{ID: "demo.item.reveal"}
	g := grant.Grant{Target: "demo.item.reveal", Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: 3}

	g.Uses = 0
	e.spentOn("a", []grant.Grant{g})
	if got := e.ended("a", c, "", grant.Caller{}, time.Now()); got != "" {
		t.Errorf("one use of three was described as the grant ending: %q", got)
	}
	g.Uses = 2
	e.spentOn("a", []grant.Grant{g})
	if got := e.ended("a", c, "", grant.Caller{}, time.Now()); !strings.Contains(got, "(3 of 3 uses)") {
		t.Errorf("the last use was not described as the grant ending: %q", got)
	}
	g.Uses = 0
	e.spentOn("a", []grant.Grant{g})
	if got := e.ended("a", c, "", grant.Caller{}, time.Now()); !strings.Contains(got, "(3 of 3 uses)") {
		t.Errorf("a late, lower count took back what was already seen: %q", got)
	}
}

func TestWhatIsRememberedOfUsedGrantsIsBounded(t *testing.T) {
	e := &endedGrants{}
	for i := range 3 * maxUsedGrants {
		e.spentOn("a", []grant.Grant{{Target: "demo.item.reveal", Scope: string(rune('a'+i%26)) + string(rune('A'+i/26)),
			Issued: time.Now(), Expires: time.Now().Add(time.Hour)}})
	}
	if n := len(e.used); n != maxUsedGrants {
		t.Errorf("%d grants remembered, want the %d most recent", n, maxUsedGrants)
	}
}
