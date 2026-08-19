package kv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/mcp"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
)

// mcpSession stands the real bridge up over an in-memory transport against a
// registry holding the real kv plugin, and hands back a connected client.
// Nothing is stubbed between the call and the handler: these tests are about
// what an agent can reach through the bridge, and a stand-in for the bridge
// would only prove what the stand-in does.
func mcpSession(t *testing.T, opts mcp.Options) *sdk.ClientSession {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(Plugin()); err != nil {
		t.Fatal(err)
	}
	server := mcp.NewServer(reg, "test", opts)
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// toolProperties returns one tool's published input properties: the list of
// things an agent is told it may send.
func toolProperties(t *testing.T, session *sdk.ClientSession, tool string) map[string]any {
	t.Helper()
	tools, err := session.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range tools.Tools {
		if tl.Name != tool {
			continue
		}
		props, _ := tl.InputSchema.(map[string]any)["properties"].(map[string]any)
		return props
	}
	t.Fatalf("tool %q is not exposed", tool)
	return nil
}

// grantFor issues a grant the way an operator would — straight to the store,
// since the capability that issues them is deliberately out of an agent's
// reach.
func grantFor(t *testing.T, target, scope string) {
	t.Helper()
	if verr := grant.Save([]grant.Grant{{
		Target: target, Scope: scope,
		Issued: time.Now(), Expires: time.Now().Add(15 * time.Minute),
	}}); verr != nil {
		t.Fatalf("issuing the grant: %v", verr)
	}
}

// The real attack, end to end.
//
// A grant on kv.get authorizes revealing one value. --out is a path on the
// machine running rta, chosen entirely by the caller — and until this was
// marked Local, an MCP agent could choose it, turning "reveal db-password"
// into "overwrite any file I can name with db-password's contents". This
// drives the actual MCP bridge (not a stand-in) against the real kv plugin,
// with a grant issued the way an operator would issue one, to prove the
// path an agent would actually take is closed — not just that the field is
// annotated Local.
func TestOutCannotBeAimedAtAnArbitraryFileOverMCP(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)
	// A real MCP server unlocks the store from its own environment (the
	// operator's decision when launching it) — passphraseField is Local, so
	// it never arrives as an argument.
	t.Setenv(passphraseEnv, "correct horse battery staple")
	grantFor(t, "kv.get", "db-password")

	session := mcpSession(t, mcp.Options{AllowWrite: true})
	if _, offered := toolProperties(t, session, "kv_get")["out"]; offered {
		t.Fatal("out is offered in the kv_get tool schema — an agent can be told to ask for it")
	}

	victim := filepath.Join(t.TempDir(), ".bashrc")
	original := "must survive untouched"
	if err := os.WriteFile(victim, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "kv_get",
		Arguments: map[string]any{"key": "db-password", "out": victim},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}
	// The value must come back in the response, not disappear into a file
	// the caller named.
	got := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(got, "s3cret") {
		t.Errorf("value not in the response: %q", got)
	}

	after, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("the injected path was written to: %q", after)
	}
}

// kv.rm asks a person twice before an agent may destroy a secret:
// --allow-destructive names the capability, and a grant names the key.
// kv.set destroys the same secret for the same key — an overwrite replaces
// the value in place and nothing anywhere keeps the old one — and asked for
// --allow-write and nothing else. Two operations with one blast radius were
// asking two different questions, and the cheaper question was on the one
// that is not reversible.
func TestSetCannotOverwriteAStoredSecretWithoutAGrant(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	session := mcpSession(t, mcp.Options{AllowWrite: true})
	overwrite := &sdk.CallToolParams{
		Name:      "kv_set",
		Arguments: map[string]any{"key": "db-password", "value": "overwritten"},
	}
	res, err := session.CallTool(context.Background(), overwrite)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("an ungranted overwrite was accepted")
	}
	if got := text(t, runGet, map[string]any{"key": "db-password"}, false); got != "s3cret" {
		t.Fatalf("the stored secret was replaced: %q", got)
	}

	// The point is the question, not a refusal: once a person has answered
	// it for this key, the same call goes through.
	grantFor(t, "kv.set", "db-password")
	res, err = session.CallTool(context.Background(), overwrite)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted overwrite was refused: %+v", res.Content)
	}
	if got := text(t, runGet, map[string]any{"key": "db-password"}, false); got != "overwritten" {
		t.Fatalf("the granted overwrite did not land: %q", got)
	}
}

// A grant on kv.set says which key may be written. It says nothing about
// which of the host's files may be read into it, and --file was the caller's
// to choose: "store the staging token" was reachable as "store
// ~/.ssh/id_ed25519 under the name staging-token", with kv.set's own answer
// reporting the detected kind and byte size of whatever was found there.
// Local closes it — over MCP the value travels in the request or not at all —
// while a person at a terminal keeps --file exactly as before.
func TestSetFileCannotBeChosenOverMCP(t *testing.T) {
	setup(t)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	grantFor(t, "kv.set", "note")

	session := mcpSession(t, mcp.Options{AllowWrite: true})
	if _, offered := toolProperties(t, session, "kv_set")["file"]; offered {
		t.Fatal("file is offered in the kv_set tool schema — an agent can be told to ask for it")
	}

	secret := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(secret, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nnot really\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "kv_set",
		Arguments: map[string]any{"key": "note", "value": "just a note", "file": secret},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}

	// --file wins over the positional value in runSet, so a path that got
	// through would be visible here as the stored content.
	if got := text(t, runGet, map[string]any{"key": "note"}, false); got != "just a note" {
		t.Fatalf("the caller's path was read into the store instead of the value it sent: %q", got)
	}
}
