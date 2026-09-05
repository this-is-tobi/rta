package agent

import (
	"context"
	"testing"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// **A grant an agent issued itself must not look like one you issued** — and
// this path assumed it always was one. `agent allow --ttl` stamped FromForm
// unconditionally, on the argument that answering a parked request is always
// a person; but the capability runs from any shell, so an agent that parked a
// call through its own MCP session could answer it and mint a standing grant
// recorded as the *most* trusted origin of the three, while `grant allow`
// was carefully writing `command` for the identical act. The origin heuristic
// is only worth having if every issuing path takes the same measurement.
func TestAnsweringFromAShellDoesNotRecordAPerson(t *testing.T) {
	isolate(t)
	r := park(t, "kv.get", "db-password")

	// The test harness is the attack shape: SurfaceCLI with no terminal on
	// fd 0 — exactly what `rta agent allow` looks like from an agent's shell
	// tool.
	if _, err := run(t, "agent.allow", map[string]any{"id": r.ID, "ttl": "15m"}); err != nil {
		t.Fatal(err)
	}
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 1 {
		t.Fatalf("%d grants issued, want one", len(grants))
	}
	if got := grants[0].From; got != grant.FromCommand {
		t.Fatalf("a grant issued with nobody there records origin %q, want %q", got, grant.FromCommand)
	}
}

// The other direction: the TUI dispatches this same capability, and a TUI is
// a person whatever the file descriptor says — the same clause the origin
// table in builtin/grant pins.
func TestAnsweringFromTheTUIRecordsAForm(t *testing.T) {
	isolate(t)
	r := park(t, "kv.get", "db-password")

	c := capability(t, "agent.allow")
	req := plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{
		Caller: map[string]any{"id": r.ID, "ttl": "15m"},
	}), false, false).WithSurface(plugin.SurfaceTUI)
	if _, err := c.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 1 {
		t.Fatalf("%d grants issued, want one", len(grants))
	}
	if got := grants[0].From; got != grant.FromForm {
		t.Fatalf("a grant issued from a TUI form records origin %q, want %q", got, grant.FromForm)
	}
}
