package note

import (
	"context"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/registry"
)

func mcpSession(t *testing.T) *sdk.ClientSession {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(Plugin()); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(reg, "test", mcp.Options{})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool(t *testing.T, session *sdk.ClientSession, tool string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].(*sdk.TextContent).Text, res.IsError
}

// The several-ids shape end to end, through the real bridge: a grant for one
// note carries a call naming that note and no call naming another beside it,
// and an id that is not the array of strings the schema publishes is refused
// before the gate, so what the gate judged is what the handler acts on.
func TestAnAgentNamingSeveralNotesNeedsACoverForEachOverMCP(t *testing.T) {
	setup(t)
	addNotes(t, "a", "b", "c")
	if verr := grant.Save([]grant.Grant{{
		Target: "note.rm", Scope: "1",
		Issued: time.Now(), Expires: time.Now().Add(15 * time.Minute),
	}}); verr != nil {
		t.Fatal(verr)
	}
	session := mcpSession(t)

	text, isErr := callTool(t, session, "note_rm", map[string]any{"id": []string{"1", "2"}})
	if !isErr || !strings.Contains(text, "core.grant.required") || !strings.Contains(text, "no active grant for note.rm 2") {
		t.Errorf("a call naming notes 1 and 2 under a grant for 1: error=%v %s", isErr, text)
	}
	if s, _ := load(); len(s.Items) != 3 {
		t.Fatalf("a refused call removed notes: %+v", s.Items)
	}

	for name, id := range map[string]any{"a number": 1, "a number in the array": []any{1}, "a mixed array": []any{"1", 2}} {
		text, isErr := callTool(t, session, "note_rm", map[string]any{"id": id})
		if !isErr || !strings.Contains(text, "core.mcp.badargs") {
			t.Errorf("%s: error=%v %s, want core.mcp.badargs", name, isErr, text)
		}
	}
	if s, _ := load(); len(s.Items) != 3 {
		t.Fatalf("a malformed call removed notes: %+v", s.Items)
	}

	text, isErr = callTool(t, session, "note_rm", map[string]any{"id": []string{"1"}})
	if isErr || !strings.Contains(text, "removed note 1: a") {
		t.Errorf("the note the grant names: error=%v %s", isErr, text)
	}
	if s, _ := load(); len(s.Items) != 2 {
		t.Errorf("after the granted call: %+v", s.Items)
	}
}
