package grant

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/view"
)

// twoServers stores what two servers of one account hold in their shared
// grant file: staging's agent two grants, prod's two, one of prod's on a
// plugin staging does not load.
func twoServers(t *testing.T) {
	t.Helper()
	now := time.Now()
	for _, g := range []core.Grant{
		{Target: "kv.get", Agent: "staging"},
		{Target: "kv.env", Agent: "staging"},
		{Target: "kv.get", Scope: "db-password", Agent: "prod"},
		{Target: "vault.kv.get", Scope: "payments", Agent: "prod"},
	} {
		g.Issued, g.Expires, g.TTL = now, now.Add(15*time.Minute), "15m"
		if verr := core.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
}

// prodsGrants is what is left standing for prod's agent.
func prodsGrants(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, g := range standing(t) {
		if g.Agent == "prod" {
			out = append(out, g.Target+" "+g.Scope)
		}
	}
	slices.Sort(out)
	return out
}

// The grant file is every server's of an account, and a roster is one
// server's: an operator enrolled on staging took back prod's grants with
// --all or --agent prod, and staging's channel answered a mistyped or foreign
// name from prod's grants. The server halves answer for their own agent.
func TestAServersChannelTakesBackItsOwnAgentsGrantsAlone(t *testing.T) {
	setup(t)
	twoServers(t)
	revoke := RevokeRemote("staging", catalog)
	want := []string{"kv.get db-password", "vault.kv.get payments"}

	if _, verr := revoke(operatorid.RevokeSpec{Target: "kv.get", Agent: "prod"}, true); verr == nil ||
		verr.Code != "grant.agent.elsewhere" || verr.Input != "agent" {
		t.Errorf("a revoke naming prod's agent through staging: %v, want grant.agent.elsewhere on agent", verr)
	}
	// A foreign name is unknown here whoever holds it there, and a typo is not
	// answered with the name of another server's grant.
	for _, target := range []string{"vault.kv.get", "vault.kv.gett"} {
		_, verr := revoke(operatorid.RevokeSpec{Target: target}, true)
		if verr == nil || verr.Code != "grant.unknowntarget" {
			t.Errorf("revoking %s through staging: %v, want grant.unknowntarget", target, verr)
		} else if strings.Contains(verr.Hint, "vault") {
			t.Errorf("revoking %s through staging hints at prod's grant: %q", target, verr.Hint)
		}
	}
	// A record prod holds a grant on is not staging's to take back: its revoke
	// through staging takes nothing, and names staging's grant that still
	// covers the record.
	if out, verr := revoke(operatorid.RevokeSpec{Target: "kv.get", Scope: "db-password"}, false); verr != nil ||
		out.Revoked != 0 || out.Still == nil || out.Still.Agent != "staging" {
		t.Errorf("a dry revoke of a record prod holds: %+v, %v, want staging's covering grant and nothing taken", out, verr)
	}

	out, verr := revoke(operatorid.RevokeSpec{All: true}, true)
	if verr != nil || out.Revoked != 2 || out.Agent != "staging" {
		t.Fatalf("--all through staging: %+v, %v, want staging's two grants, said to be staging's", out, verr)
	}
	if got := prodsGrants(t); !slices.Equal(got, want) {
		t.Errorf("--all through staging left prod %q, want %q", got, want)
	}
	if out, verr := revoke(operatorid.RevokeSpec{All: true}, false); verr != nil || out.Revoked != 0 || out.NoneActive != true {
		t.Errorf("with staging's grants gone: %+v, %v, want none active, as staging sees it", out, verr)
	}
}

// Issuance is held to the same rule: the guard's signing set is the
// machine's, so a draft for prod's agent prepared through staging would be
// signed and honoured.
func TestAServersChannelPreparesForItsOwnAgentAlone(t *testing.T) {
	setup(t)
	prepare := PrepareRemote("staging", catalog, builtIn)
	if _, verr := prepare(operatorid.IssueSpec{Target: "kv.get", Agent: "prod", TTL: "15m"}, "tobi"); verr == nil ||
		verr.Code != "grant.agent.elsewhere" {
		t.Errorf("a draft for prod's agent through staging: %v, want grant.agent.elsewhere", verr)
	}
	p, verr := prepare(operatorid.IssueSpec{Target: "kv.get", Agent: "staging", TTL: "15m"}, "tobi")
	if verr != nil || p.Grant.Agent != "staging" {
		t.Errorf("a draft for staging's own agent: %+v, %v", p.Grant, verr)
	}
}

// What the operator reads: staging's roster, and a revoke said to be of
// staging's grants, since a server's answer lists none of them.
func TestARemoteRosterAndRevokeAreTheServersOwnAgents(t *testing.T) {
	setup(t)
	remoteLab(t, "staging")
	twoServers(t)
	tbl := remoteRoster(t)
	if len(tbl.Rows) != 2 {
		t.Fatalf("staging's roster has %d rows, want its two: %v", len(tbl.Rows), tbl.Rows)
	}
	for _, row := range tbl.Rows {
		if strings.Contains(strings.Join(row, " "), "prod") || strings.Contains(strings.Join(row, " "), "vault") {
			t.Errorf("staging's roster shows prod's grant: %v", row)
		}
	}
	v, err := guardCap(t, "grant.revoke").Run(context.Background(),
		reqTUI(map[string]any{"all": true, "server": "lab", "passphrase": "correct horse"}))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.HasPrefix(body, "revoked 2 grants for agent staging") {
		t.Errorf("--all through staging said %q, want the count said to be staging's", body)
	}
	if got := prodsGrants(t); len(got) != 2 {
		t.Errorf("prod's grants after staging's --all: %q", got)
	}
}
