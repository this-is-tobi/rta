package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/registry"
)

// **Two agents were one principal, and the record could not tell them apart.**
//
// Every MCP client on a machine reads the same grant file and writes the same
// ledger, so before this a grant issued while talking to one agent authorized
// every other one — measured, not assumed: two clients naming themselves
// differently both got the operator's consent, and the two entries they left
// differed only in sequence number.
//
// These tests are the two halves of that, and the second half matters as much
// as the first: an implementation that refused *everything* would pass the
// "the stranger is refused" assertion on its own.

// asAgent connects a client to a server launched under a given name, and lets
// the client announce whatever it likes about itself — which is the whole
// point of keeping the two apart.
func asAgent(t *testing.T, reg *registry.Registry, opts Options, claims string) *sdk.ClientSession {
	t.Helper()
	server := NewServer(reg, "test", opts)
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: claims, Version: "1.0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestAGrantForOneAgentDoesNotAuthorizeAnother(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := testRegistry(t)
	opts := func(name string) Options {
		return Options{Agent: name}
	}

	// Consent given while talking to one agent.
	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password", Agent: "desktop",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}

	granted := asAgent(t, reg, opts("desktop"), "some-client")
	if res := callTool(t, granted, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("the agent the grant was issued to was refused: %s",
			res.Content[0].(*sdk.TextContent).Text)
	}

	// A second agent on the same machine, reading the same grant file.
	other := asAgent(t, reg, opts("ci"), "some-client")
	res := callTool(t, other, "demo_item_reveal", map[string]any{"key": "prod/db-password"})
	if !res.IsError {
		t.Fatal("a grant issued to `desktop` authorized `ci` — every MCP client on " +
			"this machine is one principal again")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "grant") {
		t.Errorf("the refusal does not read as a missing grant, so an agent could tell "+
			"'granted to somebody else' from 'granted to nobody': %s", text)
	}
}

// The compatibility half, and the reason Agent is compared exactly rather than
// treated as "empty means anybody": a grant on disk from before agents were
// named keeps covering the unnamed server it was issued to, and covers no
// agent added afterwards.
func TestAnUnnamedGrantCoversTheUnnamedServerAndNoOtherAgent(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := testRegistry(t)

	if verr := grant.Issue(grant.Grant{
		Target: "demo.item.reveal", Scope: "prod/db-password",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}, true); verr != nil {
		t.Fatal(verr)
	}

	unnamed := asAgent(t, reg, Options{}, "some-client")
	if res := callTool(t, unnamed, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); res.IsError {
		t.Fatalf("a grant issued before anybody was named stopped covering the "+
			"unnamed server it was issued to: %s", res.Content[0].(*sdk.TextContent).Text)
	}

	named := asAgent(t, reg, Options{Agent: "ci"}, "some-client")
	if res := callTool(t, named, "demo_item_reveal",
		map[string]any{"key": "prod/db-password"}); !res.IsError {
		t.Fatal("an unnamed grant widened to cover a named agent — which is what " +
			"'empty means anybody' would have done to every grant already on disk")
	}
}

// The record answers "which agent did this", and keeps the operator's name and
// the client's claim in different fields — because anything that can speak the
// protocol can call itself whatever it likes.
func TestTheRecordSaysWhoCalledAndWhoTheyClaimedToBe(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := testRegistry(t)

	s := asAgent(t, reg, Options{Agent: "desktop"}, "totally-vault")
	callTool(t, s, "demo_item_list", map[string]any{"name": "x"})

	entries, err := agentlog.Read(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("nothing was recorded")
	}
	e := entries[len(entries)-1]
	if e.Agent != "desktop" {
		t.Errorf("Agent = %q, want the name the server was launched under", e.Agent)
	}
	if !strings.HasPrefix(e.Client, "totally-vault") {
		t.Errorf("Client = %q, want the name the caller announced for itself", e.Client)
	}
	// The two are separate fields on purpose. A client that renames itself
	// must not be able to overwrite what the operator wrote down.
	if e.Agent == e.Client {
		t.Error("the operator's name and the client's claim collapsed into one value")
	}
}

// A hostile clientInfo is a display problem, not a call-failing one: it is
// cleaned and bounded, and the call goes through.
func TestAHostileClientNameIsCleanedAndBounded(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	reg := testRegistry(t)

	hostile := "\x1b]8;;http://evil\x07vault\x1b]8;;\x07" + strings.Repeat("é", 200)
	s := asAgent(t, reg, Options{}, hostile)
	if res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"}); res.IsError {
		t.Fatal("a call failed because of what its client called itself")
	}

	entries, err := agentlog.Read(10)
	if err != nil {
		t.Fatal(err)
	}
	got := entries[len(entries)-1].Client
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("an escape sequence reached the record: %q", got)
	}
	if len(got) > maxClientName+len("…") {
		t.Errorf("Client is %d bytes, want it bounded near %d — every entry carries it",
			len(got), maxClientName)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncation cut a multi-byte rune in half: %q", got)
	}
}
