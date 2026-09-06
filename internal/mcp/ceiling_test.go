package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/policy"
)

// **The ceiling has to be enforced on the way out, not only where a grant is
// issued.** That is the rule Active() already states for MaxTTL: checking it
// at issue alone leaves the cap in the CLI and trusts the file to have been
// written by it. So these drive a grant that is already on disk — issued
// before the policy existed, or written by something else — through the real
// server, and assert that the policy stops it.

// withPolicy writes a team policy above the working directory and returns.
func withPolicy(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	// So a policy file belonging to whoever runs the tests cannot join in.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

func TestAPolicyStopsAGrantThatIsAlreadyOnDisk(t *testing.T) {
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	// Consent stands, and the call runs.
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("the grant did not work before the policy existed: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	// The team tightens. Nothing about the grant file changes.
	withPolicy(t, "never: [demo.item.reveal]\n")
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); !res.IsError {
		t.Fatal("a live grant on a target the team's policy forbids still authorized " +
			"the call — the ceiling is only being checked where grants are issued")
	}
}

// The other half, and the one an implementation that refused everything would
// fail: a policy narrows and does not black out the surface.
func TestAPolicyLeavesEverythingItDoesNotName(t *testing.T) {
	withPolicy(t, "never: [pg.dump]\nneverProfile: [prod]\n")
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("a policy naming other things refused this one: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}
}

// A maxTTL in the policy has to bite a grant issued for longer, whatever the
// grant's own Expires says — the same reasoning MaxTTL's own check gives for
// a hand-written grant claiming to expire in 2099.
func TestAPolicyMaxTTLExpiresAGrantIssuedForLonger(t *testing.T) {
	s := connect(t, Options{})
	// Issued two hours ago for four hours: comfortably live by its own terms.
	issued := time.Now().Add(-2 * time.Hour)
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password",
		Issued: issued, Expires: issued.Add(4 * time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("the grant was not live to begin with: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	withPolicy(t, "maxTTL: 15m\n")
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); !res.IsError {
		t.Fatal("a grant issued two hours ago survived a 15m team ceiling")
	}
}

// **The unscoped grant is the one this is really for.** `kv.get` with no
// record authorizes the whole store, which is the pressure folder scopes
// exist to relieve — a team can now decide it is not available.
func TestRequireScopeRefusesTheEverythingGrantAndKeepsTheScopedOne(t *testing.T) {
	s := connect(t, Options{})
	now := time.Now()
	// Both issued before the policy exists — which is also the only way to
	// get the unscoped one onto disk, because Issue refuses it once the
	// policy is in force. The two halves of the mechanism agreeing is the
	// point: it refuses where somebody can fix it, and it stops working for
	// what is already there.
	for _, scope := range []string{"", "prod/db-password"} {
		if verr := grant.Issue(grant.Grant{
			Target: "demo.item.reveal", Scope: scope,
			Issued: now, Expires: now.Add(time.Hour),
		}, true); verr != nil {
			t.Fatal(verr)
		}
	}

	withPolicy(t, "requireScope: [demo.item.reveal]\n")
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "anything-at-all"}); !res.IsError {
		t.Fatal("an unscoped grant authorized a call where the policy requires a record")
	}
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("requireScope refused a grant that names a record: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	// And issuing a new one is refused where the person is standing.
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal",
		Issued: now, Expires: now.Add(time.Hour),
	}, true); verr == nil {
		t.Fatal("an unscoped grant was issued under a policy requiring a record")
	}
}

// A ceiling can only subtract, so a malformed one must not silently become no
// ceiling — but neither may it be readable as "allowed". Refusing on the
// authorization path is the fail-closed direction.
func TestAMalformedPolicyDoesNotSilentlyBecomeNoCeiling(t *testing.T) {
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	withPolicy(t, "never: \"this should be a list\"\nmaxTTL: {}\n")
	if res := callTool(t, s, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); !res.IsError {
		t.Fatal("a policy file that could not be parsed was treated as no policy, " +
			"which is a bound reporting itself without running")
	}
}
