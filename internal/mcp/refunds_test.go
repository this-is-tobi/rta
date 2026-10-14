package mcp

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/lockdown"
	"github.com/this-is-tobi/rta/pkg/view"
)

// usesSpent is what the only grant in the store has spent.
func usesSpent(t *testing.T) int {
	t.Helper()
	grants, verr := grant.Load()
	if verr != nil || len(grants) != 1 {
		t.Fatalf("the grants after the call: %+v (%v), want the one still there", grants, verr)
	}
	return grants[0].Uses
}

// What the docs say about `--max-uses` is a list of what gives a use back, and
// each item is a place the bridge refuses a call after the gate has spent its
// use and before the capability runs. A profile that does not resolve and a
// credential it cannot fill are two of them, and a lock is not one: it refuses
// before anything is reserved, so there is nothing to give back. The fence, the
// guard's refusal and the handler's own failure have a test where they are
// exercised (TestAFencedRefusalDoesNotSpendTheGrant, TestAConfigValueTheGuard
// RefusesSpendsNoGrant, TestOneTimeGrantIsSpentByAFailedCall).
func TestACallRefusedBeforeTheCapabilityRanKeepsItsUse(t *testing.T) {
	t.Run("a profile that does not resolve", func(t *testing.T) {
		f := newFenceFixture(t, `
profiles:
  broken:
    plugins:
      app:
        only:
          nothere: [x]
`, t.TempDir())
		f.grantFor(t, "broken", 1)
		// The agent hears an ungranted call's words, whatever is wrong with the
		// profile; the row says what it was.
		assertCode(t, f.call(t, map[string]any{"profile": "broken"}), "core.grant.required")
		if entries, err := agentlog.Read(1); err != nil || len(entries) != 1 || entries[0].Code != "core.profile.unusable" {
			t.Fatalf("the ledger recorded %+v (%v), want the call refused as an unusable profile", entries, err)
		}
		if got := usesSpent(t); got != 0 {
			t.Errorf("the grant spent %d use on a profile that never resolved", got)
		}
	})

	t.Run("a credential the profile could not fill", func(t *testing.T) {
		f := newProfileFixture(t, `
profiles:
  staging:
    plugins:
      pg:
        set:
          host: staging.internal
        secrets:
          password: kv:staging-db-password
`, func(o *Options) {
			o.Secrets = func(string) (string, *view.Error) {
				return "", view.Errorf("kv.notfound", "no entry")
			}
		})
		now := time.Now()
		if verr := grant.Save([]grant.Grant{{
			Target: "pg", Profile: "staging", ProfilePin: f.pin("staging", "pg"),
			Issued: now, Expires: now.Add(time.Hour), MaxUses: 1,
		}}); verr != nil {
			t.Fatal(verr)
		}
		assertCode(t, f.call(t, map[string]any{"profile": "staging", "sql": "select 1"}), "core.profile.secret.unavailable")
		if got := usesSpent(t); got != 0 {
			t.Errorf("the grant spent %d use on a credential nothing filled", got)
		}
	})

	t.Run("a lock, which refuses before the use is reserved", func(t *testing.T) {
		s := connect(t, Options{Agent: "claude"})
		now := time.Now()
		if verr := grant.Save([]grant.Grant{{
			Target: "demo.item.reveal", Scope: "staging", Agent: "claude",
			Issued: now, Expires: now.Add(time.Hour), MaxUses: 1,
		}}); verr != nil {
			t.Fatal(verr)
		}
		l, verr := lockdown.Build("agent", "claude", "runaway loop", "", "terminal")
		if verr != nil {
			t.Fatal(verr)
		}
		if verr := lockdown.Add(l); verr != nil {
			t.Fatal(verr)
		}
		assertCode(t, callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}), "core.lock.frozen")
		if got := usesSpent(t); got != 0 {
			t.Errorf("the grant spent %d use on a call a lock refused", got)
		}
	})
}
