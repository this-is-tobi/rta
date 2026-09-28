package grant

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// neighbours stands the grants a roster row naming no record sits among:
// the one it is, and each grant a selector left out would sweep in beside
// it — a record, a connection, another agent, the whole plugin.
func neighbours(t *testing.T) {
	t.Helper()
	now := time.Now()
	for _, g := range []core.Grant{
		{Target: "kv.get", Agent: "claude"},
		{Target: "kv.get", Scope: "db-password", Agent: "claude"},
		{Target: "kv.get", Profile: "staging", Agent: "claude"},
		{Target: "kv.get", Agent: "cursor"},
		{Target: "kv", Agent: "claude"},
	} {
		g.Issued, g.Expires, g.TTL = now, now.Add(15*time.Minute), "15m"
		if verr := core.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
}

// named is a grant as this test tells them apart.
func named(g core.Grant) string {
	return strings.Join([]string{g.Target, g.Scope, g.Profile, g.Agent}, "|")
}

// renewedTo is every standing grant renewal took to ttl, by name.
func renewedTo(t *testing.T, ttl string) []string {
	t.Helper()
	var out []string
	for _, g := range standing(t) {
		if g.TTL == ttl {
			out = append(out, named(g))
		}
	}
	slices.Sort(out)
	return out
}

func standingNames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, g := range standing(t) {
		out = append(out, named(g))
	}
	slices.Sort(out)
	return out
}

// A renewal naming no record, with exact, extends the grant naming no
// record and no other: the grant on a record beside it, the one on another
// connection and the one on the whole plugin keep their deadlines. Without
// exact the same selectors extend every kv.get grant claude holds, which is
// what n on a roster row reading "any" did.
func TestAnExactRenewExtendsTheOneGrantNamed(t *testing.T) {
	setup(t)
	neighbours(t)
	if _, err := renew(t, map[string]any{"target": "kv.get", "agent": "claude", "ttl": "1h", "exact": true}); err != nil {
		t.Fatal(err)
	}
	if got := renewedTo(t, "1h"); !slices.Equal(got, []string{"kv.get|||claude"}) {
		t.Errorf("an exact renew extended %q, want the grant naming no record on the base connection alone", got)
	}

	if _, err := renew(t, map[string]any{"target": "kv", "agent": "claude", "ttl": "2h", "exact": true}); err != nil {
		t.Fatal(err)
	}
	if got := renewedTo(t, "2h"); !slices.Equal(got, []string{"kv|||claude"}) {
		t.Errorf("an exact renew of the plugin extended %q, want its grant on the whole plugin alone", got)
	}

	if _, err := renew(t, map[string]any{"target": "kv.get", "agent": "claude", "ttl": "4h"}); err != nil {
		t.Fatal(err)
	}
	if got := renewedTo(t, "4h"); len(got) != 3 {
		t.Errorf("a renew without exact extended %q, want every kv.get grant claude holds", got)
	}
}

// An exact revoke takes back the one grant named, a plugin name its grant
// on the whole plugin rather than everything in it; the answer for a grant
// that is not there names every part it looked for.
func TestAnExactRevokeTakesBackTheOneGrantNamed(t *testing.T) {
	setup(t)
	neighbours(t)
	before := standingNames(t)
	body := run(t, runRevoke, map[string]any{"target": "kv", "agent": "claude", "exact": true}).(view.Text).Body
	if !strings.HasPrefix(body, "revoked 1 grant") {
		t.Errorf("an exact revoke said %q", body)
	}
	if got := standingNames(t); len(got) != len(before)-1 || slices.Contains(got, "kv|||claude") {
		t.Errorf("an exact revoke of kv left %q, want only the grant on the whole plugin gone", got)
	}
	run(t, runRevoke, map[string]any{"target": "kv.get", "agent": "claude", "exact": true})
	if got := standingNames(t); slices.Contains(got, "kv.get|||claude") || !slices.Contains(got, "kv.get|db-password||claude") ||
		!slices.Contains(got, "kv.get||staging|claude") || !slices.Contains(got, "kv.get|||cursor") {
		t.Errorf("an exact revoke of the grant naming no record left %q", got)
	}
	body = run(t, runRevoke, map[string]any{"target": "kv.get", "agent": "claude", "exact": true}).(view.Text).Body
	if want := "No active grant is exactly kv.get on no record, on the base connection, for claude."; body != want {
		t.Errorf("revoking it again said %q, want %q", body, want)
	}
	body = run(t, renewH, map[string]any{"target": "kv.get", "scope": "prod", "profile": "staging", "exact": true}).(view.Text).Body
	if want := "Nothing to renew — no active grant is exactly kv.get on prod, via profile staging, for no named agent."; body != want {
		t.Errorf("an exact renew of nothing said %q, want %q", body, want)
	}
	// A role still narrows, so it is part of what was not found: the grant
	// on db-password stands, under no role, and "no active grant is exactly
	// kv.get on db-password" beside it on the roster read as a revoke that
	// could not see it.
	body = run(t, runRevoke, map[string]any{"target": "kv.get", "scope": "db-password", "agent": "claude",
		"role": "dev", "exact": true}).(view.Text).Body
	if want := "No active grant is exactly kv.get on db-password, on the base connection, for claude, " +
		"under role dev."; !strings.HasPrefix(body, want) {
		t.Errorf("an exact revoke under a role that matched nothing said %q, want %q", body, want)
	}
	if got := standingNames(t); !slices.Contains(got, "kv.get|db-password||claude") {
		t.Errorf("an exact revoke under a role the grant does not have took it back: %q", got)
	}
}

// exact names one grant, so it needs a target, which every grant has, and
// refuses --all, which names them all.
func TestAnExactSelectorThatCanNameNoGrantIsRefused(t *testing.T) {
	setup(t)
	for name, c := range map[string]struct {
		h      plugin.Handler
		values map[string]any
		code   string
	}{
		"revoke with no target": {runRevoke, map[string]any{"agent": "claude", "exact": true}, "grant.exact.target"},
		"renew with no target":  {renewH, map[string]any{"exact": true}, "grant.exact.target"},
		"revoke with all":       {runRevoke, map[string]any{"target": "kv.get", "all": true, "exact": true}, "grant.exact.all"},
	} {
		_, err := c.h(context.Background(), req(c.values))
		if got := code(t, err); got != c.code {
			t.Errorf("%s: %v, want %s", name, err, c.code)
		}
	}
	if _, verr := RevokeRemote(operatorid.RevokeSpec{Exact: true, Agent: "claude"}, true); verr == nil ||
		verr.Code != "grant.exact.target" {
		t.Errorf("the operator channel's exact revoke with no target: %v, want grant.exact.target", verr)
	}
}

// exact crosses the operator channel, and the server says it matched it.
// A server that ignores the field — one older than it — would read the
// selectors left out as every grant, so the client asks first, with write
// off, and refuses when the answer does not say exact: the x on a remote
// roster's row took back each grant on its target and agent.
func TestAnExactRemoteRevokeIsRefusedByAServerThatIgnoresIt(t *testing.T) {
	setup(t)
	remoteLab(t)
	neighbours(t)
	values := map[string]any{"target": "kv.get", "agent": "claude", "exact": true,
		"server": "lab", "passphrase": "correct horse"}
	v, err := guardCap(t, "grant.revoke").Run(context.Background(), reqTUI(values))
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.HasPrefix(body, "revoked 1 grant") {
		t.Errorf("an exact remote revoke said %q", body)
	}
	if got := standingNames(t); slices.Contains(got, "kv.get|||claude") || len(got) != 4 {
		t.Errorf("an exact remote revoke left %q, want the grant naming no record gone alone", got)
	}

	setup(t)
	var wrote bool
	remoteLabAs(t, mcp.OperatorConfig{Revoke: func(spec operatorid.RevokeSpec, write bool) (operatorid.RevokeOutcome, *view.Error) {
		// As an older server answers: the field unread, so no Exact said back.
		wrote = wrote || write
		spec.Exact = false
		out, verr := RevokeRemote(spec, false)
		out.Exact = false
		return out, verr
	}})
	neighbours(t)
	_, err = guardCap(t, "grant.revoke").Run(context.Background(), reqTUI(values))
	if got := code(t, err); got != "grant.remote.exact" {
		t.Fatalf("a server that ignores exact: %v, want grant.remote.exact", err)
	}
	if wrote {
		t.Error("the revoke was sent for writing after the server's answer said it ignores exact")
	}
}
