package grant

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// The matcher's own table. internal/mcp proves what an agent actually gets;
// this carries the cases that are cheap to state and easy to get wrong.
func TestAgentIsMatchedExactlyInBothDirections(t *testing.T) {
	g := func(agent string) Grant { return Grant{Target: "kv.get", Agent: agent} }
	for _, tc := range []struct {
		name  string
		grant Grant
		by    Caller
		want  bool
	}{
		{"the agent it was issued to", g("desktop"), Caller{Agent: "desktop"}, true},
		{"a different agent", g("desktop"), Caller{Agent: "ci"}, false},
		// The half that makes the field's arrival safe for grants already on
		// disk, and the half that would be a silent widening if it were true.
		{"an unnamed grant, an unnamed caller", g(""), Caller{}, true},
		{"an unnamed grant, a named caller", g(""), Caller{Agent: "ci"}, false},
		{"a named grant, an unnamed caller", g("ci"), Caller{}, false},
		// No prefix relaxation here, unlike Scope: an agent name is a whole
		// name, and "ci" must not reach "ci-prod".
		{"a name that merely starts the same", g("ci"), Caller{Agent: "ci-prod"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.grant.covers("kv.get", "", tc.by); got != tc.want {
				t.Errorf("covers = %v, want %v", got, tc.want)
			}
		})
	}
}

// **Issue's replace-equivalent rule has to distinguish exactly what covers()
// distinguishes**, which is stated in Issue's own comment and is why this
// exists: without Agent in the key, allowing a capability for one agent would
// silently revoke the grant another one was already holding.
func TestIssuingForOneAgentDoesNotRevokeAnother(t *testing.T) {
	setup(t)
	now := time.Now()
	base := Grant{Target: "kv.get", Scope: "db", Issued: now, Expires: now.Add(time.Hour)}

	first := base
	first.Agent = "desktop"
	if verr := Issue(first, true); verr != nil {
		t.Fatal(verr)
	}
	second := base
	second.Agent = "ci"
	if verr := Issue(second, true); verr != nil {
		t.Fatal(verr)
	}

	held, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(held) != 2 {
		t.Fatalf("the file holds %d grants, want 2 — granting to `ci` revoked what "+
			"`desktop` was already allowed", len(held))
	}

	// And the rule still holds where it should: re-issuing for the same agent
	// replaces rather than accumulates.
	again := base
	again.Agent = "ci"
	again.Note = "the same decision, taken again"
	if verr := Issue(again, true); verr != nil {
		t.Fatal(verr)
	}
	held, verr = Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(held) != 2 {
		t.Fatalf("the file holds %d grants, want 2 — re-issuing the same grant "+
			"stopped replacing it", len(held))
	}
}

// A grant naming an agent still has to spend a use like any other, and the
// refund has to find the same row: identity() carries Agent for that reason.
func TestAUseSpentByOneAgentIsNotChargedToAnother(t *testing.T) {
	setup(t)
	now := time.Now()
	for _, who := range []string{"desktop", "ci"} {
		if verr := Issue(Grant{
			Target: "kv.get", Scope: "db", Agent: who, MaxUses: 1,
			Issued: now, Expires: now.Add(time.Hour),
		}, true); verr != nil {
			t.Fatal(verr)
		}
	}
	// NeedsGrant, or Reserve returns before it looks at a grant at all and
	// every assertion below passes without checking anything.
	c := plugin.Capability{
		ID: "kv.get", Safety: plugin.Write, Scope: "key", NeedsGrant: true,
		Inputs: []plugin.Field{{Name: "key", Type: plugin.String}},
	}
	values := map[string]any{"key": "db"}

	if _, verr := Reserve(c, values, Caller{Agent: "desktop"}); verr != nil {
		t.Fatalf("the first agent was refused: %v", verr)
	}
	// desktop's one use is gone. ci's must be untouched.
	if _, verr := Reserve(c, values, Caller{Agent: "ci"}); verr != nil {
		t.Fatalf("spending `desktop`'s only use also spent `ci`'s: %v", verr)
	}
	if _, verr := Reserve(c, values, Caller{Agent: "desktop"}); verr == nil {
		t.Fatal("a MaxUses:1 grant authorized a second call")
	}
}

// **Homoglyphs are the reason the charset is a rule.** Two names that render
// identically and compare differently is a grant an operator cannot audit by
// reading it.
func TestAnAgentNameCannotBeMistakenForAnother(t *testing.T) {
	for _, ok := range []string{"", "claude-desktop", "ci_2", "vscode.copilot", "A1"} {
		if verr := CheckAgent(ok); verr != nil {
			t.Errorf("CheckAgent(%q) = %v, want it accepted", ok, verr)
		}
	}
	for _, bad := range []struct{ name, why string }{
		{"claude‑desktop", "a non-breaking hyphen renders as a hyphen"},
		{"clau\u0301de", "a combining character can hide anywhere"},
		{"ci prod", "a space reads as two names"},
		{"ci\nprod", "a newline breaks the row it is printed in"},
		{"агент", "cyrillic letters that look latin"},
	} {
		if verr := CheckAgent(bad.name); verr == nil {
			t.Errorf("CheckAgent(%q) was accepted — %s", bad.name, bad.why)
		}
	}
	long := ""
	for range MaxAgentName + 1 {
		long += "a"
	}
	if verr := CheckAgent(long); verr == nil {
		t.Error("an unbounded agent name was accepted")
	}
}
