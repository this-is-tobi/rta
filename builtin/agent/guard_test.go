package agent

import (
	"testing"

	"github.com/this-is-tobi/rta/internal/consent"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/guard"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func guardOn(t *testing.T) {
	t.Helper()
	old := guard.ScryptWorkFactor
	guard.ScryptWorkFactor = 10
	t.Cleanup(func() { guard.ScryptWorkFactor = old })
	if _, verr := guard.Enable("correct horse"); verr != nil {
		t.Fatal(verr)
	}
}

// With the guard on, --ttl is minting standing authority, so it costs the
// passphrase — and it is proven *before* the decision, so a refusal leaves
// the request parked and answerable rather than consumed with the grant
// lost. The one-shot answer without --ttl stays passphrase-free: it releases
// a single call an agent with a shell could have run directly.
func TestAllowWithATTLUnderTheGuard(t *testing.T) {
	isolate(t)
	guardOn(t)
	r := park(t, "kv.get", "db-password")

	_, err := run(t, "agent.allow", map[string]any{"id": r.ID, "ttl": "15m"})
	if err == nil {
		t.Fatal("standing authority minted with no passphrase")
	}
	if verr, ok := err.(*view.Error); !ok || verr.Code != "core.guard.passphrase.required" {
		t.Fatalf("err = %v, want core.guard.passphrase.required", err)
	}
	if _, ok := consent.Find(r.ID); !ok {
		t.Fatal("a refused passphrase consumed the parked request")
	}

	// Through the TUI surface, the one that may carry the passphrase as a
	// value — the CLI refuses it on argv.
	c := capability(t, "agent.allow")
	tui := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{
		Caller: map[string]any{"id": r.ID, "ttl": "15m", "passphrase": "correct horse"},
	}), false, false).WithSurface(plugin.SurfaceTUI)
	if _, err := c.Run(t.Context(), tui); err != nil {
		t.Fatal(err)
	}
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 1 || grants[0].Sig == "" {
		t.Fatalf("loaded %+v, want one signed grant", grants)
	}
}

func TestAOneShotAllowNeedsNoPassphraseUnderTheGuard(t *testing.T) {
	isolate(t)
	guardOn(t)
	r := park(t, "kv.get", "db-password")
	if _, err := run(t, "agent.allow", map[string]any{"id": r.ID}); err != nil {
		t.Fatalf("a plain allow was gated: %v", err)
	}
	if grants, _ := grant.Load(); len(grants) != 0 {
		t.Fatal("a plain allow minted standing state")
	}
}
