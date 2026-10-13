package kv

import (
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/mcp"
)

// What an agent is offered is what kv.list is: a read that needs no grant, with
// no unlock of its own in the schema — the passphrase and the identity are the
// operator's, from the environment a server is started in — and a detail it may
// ask for.
func TestAnAgentIsOfferedTheOverviewWithNoUnlockInItsSchema(t *testing.T) {
	setup(t)
	props := toolProperties(t, mcpSession(t, mcp.Options{}), "kv_overview")
	if len(props) != 1 || props["detail"] == nil {
		t.Errorf("kv_overview takes %v, want detail and nothing that unlocks", props)
	}
}

// The agent's call opens the store from the server's environment and counts it,
// with no grant, and the answer names the places the server keeps its state as
// the bridge names them for every result — never where this machine put them.
func TestAnAgentIsToldWhatTheStoreHoldsAndNotWhereItIs(t *testing.T) {
	dir := setup(t)
	text(t, runSet, map[string]any{"key": "prod/db-password", "value": "s3cr3t-value"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	session := mcpSession(t, mcp.Options{})
	res := callTool(t, session, "kv_overview", map[string]any{"detail": true})
	if res.IsError {
		t.Fatalf("kv_overview was refused: %+v", res.Content)
	}
	answer := res.Content[0].(*sdk.TextContent).Text
	for _, want := range []string{"1 key, 1 folder", "prod/"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer lacks %q:\n%s", want, answer)
		}
	}
	for _, never := range []string{"s3cr3t-value", dir} {
		if strings.Contains(answer, never) {
			t.Errorf("the answer to an agent holds %q:\n%s", never, answer)
		}
	}
	if !strings.Contains(answer, "<data dir>/kv.age") {
		t.Errorf("the store is not named as the operator's place:\n%s", answer)
	}

	// The unlock is the operator's: a passphrase an agent names is an argument
	// the tool does not have, as it is for every kv tool.
	refusedAsUnknown(t, callTool(t, session, "kv_overview", map[string]any{"passphrase": "correct horse battery staple"}), "passphrase")
	refusedAsUnknown(t, callTool(t, session, "kv_overview", map[string]any{"identity": "/tmp/anything"}), "identity")
}

// With nothing in the server's environment that opens the store, the agent gets
// the facts about the file and the way to have it opened, worded as an agent
// can act on: the operator sets the variable, and no command line is offered.
func TestAnAgentIsToldWhoToAskWhenTheStoreCannotBeOpened(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "k", "value": "v"}, false)

	res := callTool(t, mcpSession(t, mcp.Options{}), "kv_overview", map[string]any{})
	if res.IsError {
		t.Fatalf("a store the server cannot open made the overview an error: %+v", res.Content)
	}
	answer := res.Content[0].(*sdk.TextContent).Text
	for _, want := range []string{"needs a key at hand", "ask the operator to set RTA_KV_PASSPHRASE", "locked with"} {
		if !strings.Contains(answer, want) {
			t.Errorf("the answer lacks %q:\n%s", want, answer)
		}
	}
	if strings.Contains(answer, "`rta ") || strings.Contains(answer, "--passphrase") {
		t.Errorf("an agent was offered a command line it has no terminal for:\n%s", answer)
	}
}
