package mcp

import (
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/guard"
)

// The two-rm rollback, end to end: deleting guard.json and grants.json
// together leaves the disk indistinguishable from a machine where the guard
// was never enabled — a fresh Load would honour whatever an agent then
// issues itself. The server this test talks to took its Pin at startup, and
// the pin is the one place the rollback cannot reach: the same call that
// succeeded before the deletion is refused after it, with an alarm and not
// an ungranted shrug.
func TestTheServerRefusesWhenTheGuardItStartedUnderVanishes(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	old := guard.ScryptWorkFactor
	guard.ScryptWorkFactor = 10
	t.Cleanup(func() { guard.ScryptWorkFactor = old })
	signer, verr := guard.Enable("correct horse")
	if verr != nil {
		t.Fatal(verr)
	}
	now := time.Now()
	g := grant.Grant{Target: "demo.item.reveal", Issued: now, Expires: now.Add(time.Hour)}
	grant.SignWith(signer, &g)
	if verr := grant.Issue(g, true); verr != nil {
		t.Fatal(verr)
	}

	s := connectWith(t, testRegistry(t), Options{AllowWrite: []string{"demo"}})

	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "db-password"})
	if res.IsError {
		t.Fatalf("the granted call was refused before the rollback: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	// The rollback an agent's shell can type.
	if err := os.Remove(guard.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(grant.Path()); err != nil {
		t.Fatal(err)
	}

	res = callTool(t, s, "demo_item_reveal", map[string]any{"key": "db-password"})
	if !res.IsError {
		t.Fatal("the rollback re-opened the server")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "guard") {
		t.Fatalf("the refusal does not name the guard: %s", text)
	}
}
