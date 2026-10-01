package grant

import (
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// The roster says which roles stand for which agents above its rows, so the
// screen the docs send people to before they walk away answers the day's
// question without a count by hand.
//
// The time left is the roster's own reading of the clock, so it is held to
// the grants' expiry as it stood on either side of drawing the roster, and
// the role's ttl to the grants themselves. Two hours shows as "2h" only for
// the first half second after the grants were issued and as "1h59m" from
// then on, so a runner that stalled this test that long between issuing and
// listing failed it.
func TestTheRosterSaysWhichRolesStand(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    ttl: 2h\n    grants: [kv.env, kv.get]\n")
	issue(t, map[string]any{"role": "dev"})
	before := time.Now()
	v := run(t, listH, nil)
	after := time.Now()
	s, ok := v.(view.Sections)
	if !ok || s.Items[0].ID != "roles" {
		t.Fatalf("roster = %+v, want the roles in force first", v)
	}
	force := s.Items[0].View.(view.Text).Body
	if !strings.HasPrefix(force, "dev for test — 2 grants, issued ") {
		t.Fatalf("roles in force = %q", force)
	}
	grants, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	var until time.Time
	for _, g := range grants {
		if ttl := g.Expires.Sub(g.Issued); ttl != 2*time.Hour {
			t.Errorf("%s was issued for %v, want the role's 2h", g.Target, ttl)
		}
		if g.Expires.After(until) {
			until = g.Expires
		}
	}
	latest, soonest := "expires in "+format.Duration(until.Sub(before)), "expires in "+format.Duration(until.Sub(after))
	if !strings.Contains(force, latest) && !strings.Contains(force, soonest) {
		t.Fatalf("roles in force = %q, want it to say %q as it was drawn", force, soonest)
	}
	if listed(t, v).Total != 2 {
		t.Fatalf("the rows are missing under the roles: %+v", v)
	}
}

// An operator's own role may name its agent, so the morning is one word;
// a team's file may not — which principal receives a list is the one
// decision a repository edit must not make.
func TestAnOwnRoleNamesItsAgentAndATeamsMayNot(t *testing.T) {
	configDir, repo := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    agent: codex\n    grants: [kv.env]\n")
	s := issue(t, map[string]any{"role": "dev"})
	if got := pair(receipt(s), "agent"); got != "codex" {
		t.Fatalf("agent = %q, want the role's own", got)
	}
	s = issue(t, map[string]any{"role": "dev", "agent": "claude"})
	if got := pair(receipt(s), "agent"); got != "claude" {
		t.Fatalf("--agent = %q, want the flag to win", got)
	}
	teamPolicy(t, repo, "roles:\n  ops:\n    agent: claude\n    grants: [kv.env]\n")
	verr := issueErr(t, issueReq(map[string]any{"role": "ops"}, false, true))
	if verr.Code != "policy.roleagent" {
		t.Fatalf("a team role naming an agent = %v", verr)
	}
}
