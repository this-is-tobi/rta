package app

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/mcp"
)

// What a server says at the handshake is paid for once per session by every
// client, and Claude Code keeps the first 2048 characters of it. The real
// catalogue is what has to fit: the unit tests run on a registry of a few
// tools, whose areas are a few words.
func TestTheHandshakeOfTheRealCatalogueFitsWhatAClientKeeps(t *testing.T) {
	reg, err := NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	server := mcp.NewServer(reg, "golden", mcp.Options{})
	st, ct := sdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	session, err := sdk.NewClient(&sdk.Implementation{Name: "golden", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	got := session.InitializeResult().Instructions
	t.Logf("the handshake of the built-in catalogue is %d bytes", len(got))
	if len(got) > 2048 {
		t.Errorf("the instructions are %d bytes, over the 2048 a client keeps:\n%s", len(got), got)
	}
	for _, want := range []string{"area_thing_action", "kv, ", "sys"} {
		if !strings.Contains(got, want) {
			t.Errorf("the handshake never says %q:\n%s", want, got)
		}
	}
}
