package mcp

import (
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
)

// **A folder grant, proven through the real server rather than at the
// matcher.**
//
// The unit table in internal/grant says what covers() answers. This says what
// an agent actually gets, which is the claim the operator is relying on: the
// call inside the folder runs, and the one outside it is refused in the same
// words an ungranted call has always been refused in.
//
// It matters that both halves are here. A widening that authorized everything
// would pass the first assertion alone, and that is precisely the failure this
// feature could introduce.
func TestAFolderGrantAuthorizesInsideItAndRefusesOutside(t *testing.T) {
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}

	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("a record inside the granted folder was refused: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging/db-password"})
	if !res.IsError {
		t.Fatal("a folder grant for prod/ authorized a call on staging/ — the widening " +
			"widened past its own boundary")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "grant") {
		t.Errorf("the refusal does not read as a missing grant: %s", text)
	}
}

// The two boundary bugs a bare prefix match would have, driven through the
// server. `prod-adjacent` merely starts with the same letters, and the folder's
// own name is not a record in it.
func TestAFolderGrantDoesNotCoverANeighbourThatMerelyStartsTheSame(t *testing.T) {
	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	for _, key := range []string{"prod-adjacent", "prod"} {
		if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": key}); !res.IsError {
			t.Errorf("a grant on prod/ authorized a call on %q", key)
		}
	}
}

// A traversal is refused by the folder and reachable by an exact grant, which
// is the difference between inferring on the operator's behalf and doing what
// they typed.
func TestATraversalIsRefusedByTheFolderAndAllowedByAnExactGrant(t *testing.T) {
	const weird = "prod/../staging/db-password"

	s := connect(t, Options{})
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": weird}); !res.IsError {
		t.Fatal("a folder grant covered a scope that resolves out of the folder")
	}

	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: weird,
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": weird}); res.IsError {
		t.Fatalf("an exact grant did not cover the exact string it names: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}
}
