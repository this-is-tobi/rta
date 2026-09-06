package mcp

import (
	"context"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
)

// A row the gate let through on a standing grant names the role that grant
// was issued under, and when — so a morning's dev and an afternoon's read
// apart in the record without a join against the roster.
func TestAStandingRowNamesItsRole(t *testing.T) {
	s := connect(t, Options{Agent: "claude"})
	issued := time.Now().Add(-time.Hour).Truncate(time.Second)
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.set", Agent: "claude", Role: "dev", Issued: issued, Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "demo_item_set"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted write was refused: %s", res.Content[0].(*sdk.TextContent).Text)
	}
	entries, err := agentlog.Read(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("record: %v %+v", err, entries)
	}
	e := entries[0]
	if e.Auth != agentlog.Standing || e.Role != "dev" || e.RoleIssued != issued.UTC().Format(time.RFC3339) {
		t.Fatalf("row = %+v, want role dev issued %s", e, issued.UTC().Format(time.RFC3339))
	}
}
