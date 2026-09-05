package kv

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/mcp"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/toolcall"
	"github.com/this-is-tobi/rta/pkg/plugin"
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

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
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

// A regression test for a real bug review caught and
// reproduced against this exact real bridge: --out being Local only proved
// a *caller-supplied* value could not redirect a revealed secret. Every
// Local field also resolved from the host's own environment unconditionally,
// so whoever launched `rta mcp serve` could achieve the identical redirect
// ambiently, just by having RTA_KV_OUT set — silently defeating kv.get's own
// documented invariant that an MCP caller always gets the value back in the
// response. Field.EnvFallback (opt-in, unset on --out) is the fix.
func TestOutIsNeverFilledFromTheHostEnvironmentOverMCP(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	grantFor(t, "kv.get", "db-password")

	victim := filepath.Join(t.TempDir(), "exfil")
	t.Setenv(plugin.LocalEnvVar("kv.get", "out"), victim)

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "kv_get",
		Arguments: map[string]any{"key": "db-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}
	got := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(got, "s3cret") {
		t.Errorf("value not in the response despite no out argument: %q", got)
	}
	if _, err := os.Stat(victim); err == nil {
		t.Errorf("RTA_KV_OUT silently redirected the response to %s", victim)
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

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
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

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
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

// A grant is permission to reveal a value, not permission to reach the
// operator's desk.
//
// kv.copy carries kv.get's classification because a value on a clipboard has
// been revealed, and that alone would have made it an agent's tool the moment
// --allow-write was on. But the clipboard is not a return value: the caller
// cannot read back what it wrote there, so the capability offers an agent
// nothing kv.get does not, while offering it the ability to silently replace
// the address somebody copied a second ago. Same for kv.edit, which needs a
// terminal that over MCP belongs to nobody. Both are refused with the grant
// already issued, so the refusal is the capability's own and not the gate's.
func TestCopyAndEditAreRefusedOverMCPEvenWithAGrant(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")
	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
	for _, capID := range []string{"kv.copy", "kv.edit"} {
		// One at a time: grant.Save writes the whole set, so issuing both up
		// front would leave only the second.
		grantFor(t, capID, "db-password")
		tool := toolcall.Name(capID)
		res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      tool,
			Arguments: map[string]any{"key": "db-password"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Errorf("%s went through over MCP", tool)
			continue
		}
		// The refusal has to say what to do instead, or a model retries it
		// until the context runs out.
		if got := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(got, "kv.get") &&
			!strings.Contains(got, "kv.set") {
			t.Errorf("%s refusal offers no way forward: %q", tool, got)
		}
	}
}

// kv.init's recipient is Local for a different reason than kv.get's --out or
// kv.set's --file: parseRecipient accepts a *path* and reads it, so this
// input reaches the filesystem, and the path-confinement gate only hooks
// Field.Path — a StringSlice can never be one (kv.go's own comment on the
// field says so). This drives the real bridge to prove the field is dropped,
// not merely annotated: --generate stands up a store over MCP with zero
// recipient input, and a sneaked "recipient" argument naming an unrelated
// key never reaches parseRecipient at all.
func TestInitRecipientCannotBeSuppliedOverMCP(t *testing.T) {
	setup(t)
	stray, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
	if _, offered := toolProperties(t, session, "kv_init")["recipient"]; offered {
		t.Fatal("recipient is offered in the kv_init tool schema — an agent can be told to ask for it")
	}

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "kv_init",
		Arguments: map[string]any{"generate": true, "recipient": []string{stray.Recipient().String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}

	tbl := table(t, runRecipients, nil)
	if len(tbl.Rows) != 1 {
		t.Fatalf("recipients = %v, want exactly the generated key — the sneaked recipient must not have landed", tbl.Rows)
	}
	if strings.Contains(strings.Join(tbl.Rows[0], " "), stray.Recipient().String()) {
		t.Errorf("the sneaked recipient landed in the store anyway: %v", tbl.Rows[0])
	}
}

// The same property as kv.init, on the capability named as the
// known gap: kv.rekey's recipient is Local, but Destructive already forcing
// a grant only authorises the act, not which file this reads — no kv
// capability takes a recipient from a remote caller now, which is the actual
// mitigation. Proven end to end: a store already locked to one key, rekeyed
// over the real bridge with a grant and --generate, must gain the generated
// key and nothing an agent named.
func TestRekeyRecipientCannotBeSuppliedOverMCP(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	owner, _ := writeSSHKeypair(t, dir, "id_ed25519")
	text(t, runInit, map[string]any{"identity": owner}, false)
	t.Setenv(identityEnv, owner)

	stray, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	grantFor(t, "kv.rekey", "")

	session := mcpSession(t, mcp.Options{AllowDestructive: []string{"kv.rekey"}})
	if _, offered := toolProperties(t, session, "kv_rekey")["recipient"]; offered {
		t.Fatal("recipient is offered in the kv_rekey tool schema — an agent can be told to ask for it")
	}

	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "kv_rekey",
		Arguments: map[string]any{"generate": true, "recipient": []string{stray.Recipient().String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted call was refused: %+v", res.Content)
	}

	tbl := table(t, runRecipients, map[string]any{"identity": owner})
	if len(tbl.Rows) != 2 {
		t.Fatalf("recipients = %v, want the original owner plus exactly the generated key", tbl.Rows)
	}
	joined := tbl.Rows[0][0] + " " + tbl.Rows[1][0]
	if strings.Contains(joined, stray.Recipient().String()) {
		t.Errorf("the sneaked recipient landed in the store anyway: %v", tbl.Rows)
	}
}

// kv.rm asks a person once per key before an agent may destroy a secret,
// exactly like kv.set's overwrite and kv.rename's move — proven at the same
// layer, not assumed to follow from the two of them (Destructive is gated
// through AllowDestructive rather than AllowWrite, a different exposure
// list, and review named this capability specifically as unverified here).
func TestRmNeedsAGrantForTheKeyItRemoves(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "db-password", "value": "s3cret"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	session := mcpSession(t, mcp.Options{AllowDestructive: []string{"kv.rm"}})
	call := &sdk.CallToolParams{
		Name:      "kv_rm",
		Arguments: map[string]any{"key": "db-password"},
	}
	res, err := session.CallTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("an ungranted removal was accepted")
	}
	if got := text(t, runGet, map[string]any{"key": "db-password"}, false); got != "s3cret" {
		t.Fatalf("the entry was removed anyway: %q", got)
	}

	// The point is the question, not a refusal.
	grantFor(t, "kv.rm", "db-password")
	res, err = session.CallTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted removal was refused: %+v", res.Content)
	}
	if _, err := runGet(context.Background(), req(map[string]any{"key": "db-password"}, false)); err == nil {
		t.Fatal("the entry is still readable after a granted removal")
	}
}

// Renaming reveals nothing, so the letter of the safety model would leave it
// at --allow-write. It still asks a person per key: `prod-db-password`
// renamed to `x` breaks every consumer of the name at once, and blast radius
// is what the classification is actually about.
func TestRenameNeedsAGrantForTheKeyItMoves(t *testing.T) {
	setup(t)
	text(t, runSet, map[string]any{"key": "prod-db-password", "value": "s3cret"}, false)
	t.Setenv(passphraseEnv, "correct horse battery staple")

	session := mcpSession(t, mcp.Options{AllowWrite: []string{"kv"}})
	call := &sdk.CallToolParams{
		Name:      "kv_rename",
		Arguments: map[string]any{"key": "prod-db-password", "new-name": "x"},
	}
	res, err := session.CallTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("an ungranted rename was accepted")
	}
	if got := text(t, runGet, map[string]any{"key": "prod-db-password"}, false); got != "s3cret" {
		t.Fatalf("the entry moved anyway: %q", got)
	}

	// The point is the question, not a refusal.
	grantFor(t, "kv.rename", "prod-db-password")
	res, err = session.CallTool(context.Background(), call)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the granted rename was refused: %+v", res.Content)
	}
	if got := text(t, runGet, map[string]any{"key": "x"}, false); got != "s3cret" {
		t.Errorf("the granted rename did not land: %q", got)
	}
}
