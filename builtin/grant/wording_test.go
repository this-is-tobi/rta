package grant

import (
	"context"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// `rta grant allow <tab>` offered grant.allow and the agent namespace —
// human-only, never a tool, and refused as needless by the very command
// the completion was filling in.
func TestCompletionOffersOnlyWhatAGrantCanCover(t *testing.T) {
	cat := func() []plugin.Capability {
		return []plugin.Capability{
			{ID: "sys.host", Summary: "host", Safety: plugin.Read},
			{ID: "grant.allow", Summary: "allow", Safety: plugin.Write, HumanOnly: true},
			{ID: "agent.allow", Summary: "answer", Safety: plugin.Write, HumanOnly: true},
			{ID: "kv.get", Summary: "reveal", Safety: plugin.Write},
		}
	}
	var target plugin.Field
	for _, c := range Plugin(cat, builtIn).Capabilities {
		if c.ID != "grant.allow" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "target" {
				target = f
			}
		}
	}
	if target.Suggest == nil {
		t.Fatal("the target completes nothing")
	}
	offered := strings.Join(target.Suggest(context.Background(), req(nil)), "\n")
	for _, want := range []string{"kv.get\t", "kv\tevery gated"} {
		if !strings.Contains(offered, want) {
			t.Errorf("completion lacks %q:\n%s", want, offered)
		}
	}
	for _, refused := range []string{"grant.allow", "agent.allow", "agent\t", "grant\t", "sys.host"} {
		if strings.Contains(offered, refused) {
			t.Errorf("completion offers %q, which a grant cannot cover:\n%s", refused, offered)
		}
	}
}

// The key and the line both began with the word: "guard  guard  off".
func TestTheGuardStatusDoesNotRepeatItsOwnKey(t *testing.T) {
	setup(t)
	v, err := guardCap(t, "grant.guard.status").Run(context.Background(), req(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range v.(view.KeyValue).Pairs {
		if p.Key == "guard" && strings.HasPrefix(p.Value, "guard") {
			t.Fatalf("guard = %q, repeats the key", p.Value)
		}
	}
}

// "agents may call kv.get, as agent claude" named the agent twice and the
// wrong one first; the agent is the sentence's subject.
func TestTheAgentIsTheSubjectOfTheAllowMessage(t *testing.T) {
	setup(t)
	v := run(t, allowH, map[string]any{"target": "kv.get", "agent": "claude", "ttl": "30m"})
	body := v.(view.Text).Body
	if !strings.HasPrefix(body, "claude may call kv.get for 30m") || strings.Contains(body, "as agent") {
		t.Fatalf("allow said %q", body)
	}
}
