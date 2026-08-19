package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func testRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name:    "demo",
		Summary: "demo",
		Capabilities: []plugin.Capability{
			{
				ID: "demo.item.list", Summary: "list items", Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "limit", Type: plugin.Int, Help: "max items", Default: 10},
					{Name: "name", Type: plugin.String, Help: "filter", Required: true},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: fmt.Sprintf("listed %s limit=%d", req.String("name"), req.Int("limit"))}, nil
				},
			},
			{
				ID: "demo.item.set", Summary: "set item", Safety: plugin.Write, Idempotent: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "set"}, nil
				},
			},
			{
				ID: "demo.item.rm", Summary: "remove item", Safety: plugin.Destructive,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "removed"}, nil
				},
			},
			{
				ID: "demo.item.reveal", Summary: "reveals a value", Safety: plugin.Write, Idempotent: true,
				NeedsGrant: true, Scope: "key",
				Inputs: []plugin.Field{
					{Name: "key", Type: plugin.String, Help: "which value"},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: "revealed " + req.String("key")}, nil
				},
			},
			{
				ID: "demo.item.choose", Summary: "has a closed set and a private suggestion",
				Safety: plugin.Read, Idempotent: true,
				Inputs: []plugin.Field{
					{Name: "mode", Type: plugin.String, Options: []string{"fast", "slow"}, Help: "how"},
					{Name: "kinds", Type: plugin.StringSlice, Options: []string{"a", "b"}, Help: "which"},
					{Name: "key", Type: plugin.String, Help: "which record",
						Suggest: func(context.Context, plugin.Request) []string {
							return []string{"db-password", "prod-token"}
						}},
				},
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.Text{Body: "chosen"}, nil
				},
			},
			{
				ID: "demo.item.local", Summary: "has a local credential input", Safety: plugin.Read,
				Inputs: []plugin.Field{
					{Name: "name", Type: plugin.String, Help: "a normal input"},
					{Name: "passphrase", Type: plugin.Secret, Local: true, Help: "resolved by the host"},
				},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: "passphrase=[" + req.String("passphrase") + "]"}, nil
				},
			},
			{
				// The one shape where an argument arrives under a name no
				// Field declares: "detail" is injected by the host, so an
				// unknown-argument check that reads only c.Inputs refuses the
				// richest view in the catalogue.
				ID: "demo.item.page", Summary: "has a compact and a detailed view",
				Safety: plugin.Read, Idempotent: true, Detailed: true,
				Inputs: []plugin.Field{{Name: "name", Type: plugin.String, Help: "which item"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: fmt.Sprintf("detail=%t", req.Bool("detail"))}, nil
				},
			},
			{
				ID: "demo.item.surface", Summary: "reports its calling surface", Safety: plugin.Read,
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					return view.Text{Body: "surface=" + string(req.Surface())}, nil
				},
			},
			{
				ID: "demo.item.fail", Summary: "always fails", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return nil, view.Errorf("demo.broken", "nope").WithHint("give up")
				},
			},
			{
				// Grant-gated and always fails: the one capability that lets a
				// test prove a call which passed Check but then failed inside
				// Run does not spend a one-time grant.
				ID: "demo.item.gatedfail", Summary: "needs a grant and always fails",
				Safety: plugin.Write, NeedsGrant: true,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return nil, view.Errorf("demo.broken", "nope")
				},
			},
			{
				ID: "demo.item.secret", Summary: "returns a redacted field", Safety: plugin.Read,
				Run: func(context.Context, plugin.Request) (view.View, error) {
					return view.KeyValue{
						Pairs:    []view.Pair{{Key: "user", Value: "tobi"}, {Key: "token", Value: "hunter2"}},
						Redacted: []string{"token"},
					}, nil
				},
			},
			{
				// Shaped exactly like kv.env: a StringSlice scope where naming
				// nothing means "everything", which is what turned a type
				// disagreement between the gate and the handler into a leak.
				ID: "demo.item.export", Summary: "exports named records, or all of them if none are named",
				Safety: plugin.Write, Idempotent: true, NeedsGrant: true, Scope: "key",
				Inputs: []plugin.Field{{Name: "key", Type: plugin.StringSlice, Help: "which records"}},
				Run: func(_ context.Context, req plugin.Request) (view.View, error) {
					keys := req.StringSlice("key")
					if len(keys) == 0 {
						keys = []string{"a", "b", "c"} // "all of them"
					}
					return view.Text{Body: strings.Join(keys, ",")}, nil
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// connect spins up server + client over an in-memory transport. Each session
// gets its own grant store: what an agent is allowed to do is state, and a
// test that read the developer's real grants would pass or fail by accident.
func connect(t *testing.T, opts Options) *sdk.ClientSession {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	server := NewServer(testRegistry(t), "test", opts)
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

func listTools(t *testing.T, s *sdk.ClientSession) map[string]*sdk.Tool {
	t.Helper()
	res, err := s.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]*sdk.Tool{}
	for _, tool := range res.Tools {
		tools[tool.Name] = tool
	}
	return tools
}

func TestDefaultExposesOnlyRead(t *testing.T) {
	tools := listTools(t, connect(t, Options{}))
	if _, ok := tools["demo_item_list"]; !ok {
		t.Error("read capability missing")
	}
	if _, ok := tools["demo_item_set"]; ok {
		t.Error("write capability exposed without opt-in")
	}
	if _, ok := tools["demo_item_rm"]; ok {
		t.Error("destructive capability exposed without allowlist")
	}
}

func TestOptInExposure(t *testing.T) {
	tools := listTools(t, connect(t, Options{
		AllowWrite:       true,
		AllowDestructive: []string{"demo.item.rm"},
	}))
	if _, ok := tools["demo_item_set"]; !ok {
		t.Error("write capability missing despite AllowWrite")
	}
	if _, ok := tools["demo_item_rm"]; !ok {
		t.Error("allowlisted destructive capability missing")
	}
}

func TestAnnotationsMapping(t *testing.T) {
	tools := listTools(t, connect(t, Options{
		AllowWrite:       true,
		AllowDestructive: []string{"demo.item.rm"},
	}))

	read := tools["demo_item_list"].Annotations
	if !read.ReadOnlyHint || !read.IdempotentHint {
		t.Errorf("read annotations wrong: %+v", read)
	}
	write := tools["demo_item_set"].Annotations
	if write.ReadOnlyHint || write.DestructiveHint == nil || *write.DestructiveHint {
		t.Errorf("write annotations wrong: %+v", write)
	}
	destr := tools["demo_item_rm"].Annotations
	if destr.DestructiveHint == nil || !*destr.DestructiveHint {
		t.Errorf("destructive annotations wrong: %+v", destr)
	}
}

func TestInputSchemaGeneration(t *testing.T) {
	tools := listTools(t, connect(t, Options{}))
	raw, err := json.Marshal(tools["demo_item_list"].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Type       string                    `json:"type"`
		Properties map[string]map[string]any `json:"properties"`
		Required   []string                  `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "object" {
		t.Errorf("schema type = %q", schema.Type)
	}
	if schema.Properties["limit"]["type"] != "integer" {
		t.Errorf("limit type = %v", schema.Properties["limit"]["type"])
	}
	if len(schema.Required) != 1 || schema.Required[0] != "name" {
		t.Errorf("required = %v", schema.Required)
	}
}

func TestCallToolAppliesDeclaredDefaults(t *testing.T) {
	s := connect(t, Options{})
	// "limit" (default 10) is omitted: the bridge must fill it in.
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "demo_item_list",
		Arguments: map[string]any{"name": "x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res.Content)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "limit=10") {
		t.Errorf("declared default not applied: %s", text)
	}
}

// --- Argument validation ---------------------------------------------------
//
// Nothing between json.Unmarshal and the handler enforced the schema it
// published: go-sdk's own docs say validating arguments against it is the
// caller's job, and plugin.Request's accessors return the zero value on a
// type mismatch rather than reporting one. A wrong-typed argument was
// indistinguishable from an omitted one — sys_ps {"limit": "3"} (schema:
// integer) silently returned every process at the default limit, not three,
// with no error and no warning.

// The declared type is enforced against what was actually sent, not against
// what a caller merely intended.
func TestBadArgumentTypeIsRejected(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_list", map[string]any{"name": "x", "limit": "3"})
	if !res.IsError {
		t.Fatal("a string sent where the schema says integer was accepted")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.mcp.badargs") || !strings.Contains(text, "limit") {
		t.Errorf("error does not name the offending field: %s", text)
	}
}

// A field's own default is always well-typed — validation must not choke on
// its own defaults on the way to filling them in.
func TestValidDefaultsStillApply(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_list", map[string]any{"name": "x"})
	if res.IsError {
		t.Fatalf("a call relying on its own default was rejected: %+v", res.Content)
	}
}

// A required field left out of the arguments entirely is refused, once
// defaults have had their chance to fill it — not before, so a declared
// default can satisfy its own field's requirement.
func TestMissingRequiredArgumentIsRejected(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_list", map[string]any{})
	if !res.IsError {
		t.Fatal("a required field left out entirely was accepted")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "name") {
		t.Errorf("error does not name the missing field: %s", text)
	}
}

// The enum a closed-set field publishes in its schema is enforced, not just
// offered — "PTR" is a mistake worth a schema turning into a round trip
// saved, not a value that reaches the handler unquestioned.
func TestValueOutsideDeclaredOptionsIsRejected(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_choose", map[string]any{"mode": "medium"})
	if !res.IsError {
		t.Fatal("a value outside the declared options was accepted")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "fast") || !strings.Contains(text, "slow") {
		t.Errorf("error does not list the actual options: %s", text)
	}
}

// A StringSlice field still accepts a bare string as one value — the same
// leniency plugin.Request.StringSlice itself has, and for the same reason:
// disagreeing here is exactly what let a per-key kv.env grant widen into
// exporting the whole store (the gate read the scalar as one record while
// the handler, unable to see it as a list at all, read it as none).
func TestStringSliceAcceptsAScalarButNotOtherShapes(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	allow(t, "demo.item.export", "a")

	res := callTool(t, s, "demo_item_export", map[string]any{"key": "a"})
	if res.IsError {
		t.Fatalf("a bare string in a StringSlice slot was rejected: %+v", res.Content)
	}

	res = callTool(t, s, "demo_item_export", map[string]any{"key": 42})
	if !res.IsError {
		t.Fatal("a number in a StringSlice slot was accepted")
	}
}

// An argument name the schema does not offer was accepted and dropped, so
// demo_item_list {"limt": 3} answered with the default ten items and no
// error — a one-character typo in an optional filter reading exactly like a
// filter that was applied. The refusal has to name what the tool does take,
// or the model spends the round trip the published schema was meant to save.
func TestUnknownArgumentIsRejectedAndNamesTheAcceptedOnes(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_list", map[string]any{"name": "x", "limt": 3})
	if !res.IsError {
		t.Fatal("a misspelled optional argument was accepted")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.mcp.badargs") || !strings.Contains(text, "limt") {
		t.Errorf("error does not name the offending argument: %s", text)
	}
	if !strings.Contains(text, "limit") || !strings.Contains(text, "name") {
		t.Errorf("error does not name the accepted arguments: %s", text)
	}
}

// Two mistakes are reported together and in a fixed order: a Go map iterates
// differently every run, so an unsorted list turns one wrong call into two
// different error messages.
func TestEveryUnknownArgumentIsReportedInAStableOrder(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_list", map[string]any{"name": "x", "zzz": 1, "aaa": 2})
	if !res.IsError {
		t.Fatal("two misspelled arguments were accepted")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	first, second := strings.Index(text, "aaa"), strings.Index(text, "zzz")
	if first < 0 || second < 0 {
		t.Fatalf("error does not name both unknown arguments: %s", text)
	}
	if first > second {
		t.Errorf("unknown arguments reported out of order: %s", text)
	}
}

// "detail" is the one argument that arrives under a name no Field declares.
// Refusing it as unknown would break every detail-view call on every
// Detailed capability at once — the only way an agent can reach the richest
// views in the catalogue — while a capability without a detail view has no
// business accepting it.
func TestDetailIsAcceptedOnlyWhereThereIsADetailView(t *testing.T) {
	s := connect(t, Options{})

	res := callTool(t, s, "demo_item_page", map[string]any{"detail": true})
	if res.IsError {
		t.Fatalf("a detail-view call was refused: %+v", res.Content)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "detail=true") {
		t.Errorf("detail did not reach the handler: %s", text)
	}

	if res := callTool(t, s, "demo_item_list", map[string]any{"name": "x", "detail": true}); !res.IsError {
		t.Error("detail was accepted on a capability that publishes no detail view")
	}
}

// The schema says boolean, so a string has to be refused here like anywhere
// else: plugin.Request.Bool reads "true" as false, which returned the
// compact summary looking exactly like an honoured request for the page.
func TestWrongTypedDetailIsRejected(t *testing.T) {
	s := connect(t, Options{})
	res := callTool(t, s, "demo_item_page", map[string]any{"detail": "true"})
	if !res.IsError {
		t.Fatal("a string sent where the schema says boolean was accepted")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "detail") {
		t.Errorf("error does not name the offending argument: %s", text)
	}
}

// What the bridge enforces, the schema has to say: a client that validates
// arguments against it should catch the typo before the call is made, and
// one that does not should still be able to read why it was refused.
func TestSchemaClosesTheArgumentSet(t *testing.T) {
	tools := listTools(t, connect(t, Options{}))
	schema := tools["demo_item_list"].InputSchema.(map[string]any)
	if schema["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", schema["additionalProperties"])
	}
}

// The detail view is discoverable, not folklore: an agent that cannot see
// "detail" in the schema has no way to ask for the composed page, even
// though the tool description advertises --detail in CLI syntax.
func TestDetailedCapabilitiesPublishDetail(t *testing.T) {
	c, ok := testRegistry(t).Capability("demo.item.page")
	if !ok {
		t.Fatal("missing test capability")
	}
	props := InputSchema(c)["properties"].(map[string]any)
	detail, ok := props["detail"].(map[string]any)
	if !ok {
		t.Fatalf("a Detailed capability publishes no detail property: %v", props)
	}
	if detail["type"] != "boolean" {
		t.Errorf("detail type = %v, want boolean", detail["type"])
	}

	plain, _ := testRegistry(t).Capability("demo.item.list")
	if _, published := InputSchema(plain)["properties"].(map[string]any)["detail"]; published {
		t.Error("a capability with no detail view published one anyway")
	}
}

func TestCallToolReturnsEnvelope(t *testing.T) {
	s := connect(t, Options{})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "demo_item_list",
		Arguments: map[string]any{"name": "widgets"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error result: %+v", res.Content)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("content is not a JSON envelope: %v\n%s", err, text)
	}
	if m["type"] != "text" || !strings.Contains(m["body"].(string), "widgets") {
		t.Errorf("envelope wrong: %v", m)
	}
}

func TestCallToolErrorCarriesCodeAndHint(t *testing.T) {
	s := connect(t, Options{})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "demo_item_fail"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected IsError")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatal(err)
	}
	if m["code"] != "demo.broken" || m["hint"] != "give up" {
		t.Errorf("error envelope wrong: %v", m)
	}
}

func TestToolName(t *testing.T) {
	if ToolName("pg.table.list") != "pg_table_list" {
		t.Error("ToolName mapping wrong")
	}
}

// TestCallToolRedactsSecretFields is the regression test for the redaction
// gap: MCP is a channel callers reach without a human present, so a
// KeyValue's Redacted fields must be masked exactly like every other
// renderer, not just CLI/TUI/JSON.
func TestCallToolRedactsSecretFields(t *testing.T) {
	s := connect(t, Options{})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "demo_item_secret"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res.Content)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if strings.Contains(text, "hunter2") {
		t.Fatalf("secret leaked over MCP: %s", text)
	}
	if !strings.Contains(text, "tobi") || !strings.Contains(text, view.Mask) {
		t.Errorf("expected masked envelope, got: %s", text)
	}
	// StructuredContent must be masked too — it's a second, parallel encoding
	// of the same view, easy to forget when fixing the text path.
	m, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structured content = %T", res.StructuredContent)
	}
	pairs, _ := m["pairs"].([]any)
	for _, p := range pairs {
		pair := p.(map[string]any)
		if pair["key"] == "token" && pair["value"] != view.Mask {
			t.Errorf("structured content leaked token: %v", pair)
		}
	}
}

// TestCallToolStampsTheMCPSurface: capabilities that gate on who is calling
// (kv.get requires a human-issued grant before an agent may reveal a secret)
// depend on the bridge marking every request as MCP. If this stamp ever goes
// missing, those gates silently open — an agent's request would look exactly
// like a person's.
func TestCallToolStampsTheMCPSurface(t *testing.T) {
	s := connect(t, Options{})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "demo_item_surface"})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %+v", res.Content)
	}
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "surface=mcp") {
		t.Errorf("bridge did not stamp the MCP surface: %s", text)
	}
}

// TestLocalFieldsAreNotOfferedToAgents: a credential that unlocks a tool must
// never appear in its schema. Putting one there invites a model to supply or
// invent it, and a credential that reaches a model's context has leaked
// whatever happens next — the operator supplies it to the server instead.
func TestLocalFieldsAreNotOfferedToAgents(t *testing.T) {
	c, ok := testRegistry(t).Capability("demo.item.local")
	if !ok {
		t.Fatal("missing test capability")
	}
	props := InputSchema(c)["properties"].(map[string]any)
	if _, offered := props["passphrase"]; offered {
		t.Error("a Local credential was advertised in the tool schema")
	}
	if _, offered := props["name"]; !offered {
		t.Error("ordinary inputs must still be offered")
	}
}

// A model reading "file" has every reason to think of its own working
// directory. The schema has to say whose filesystem it means.
func TestPathFieldsSayWhoseFilesystem(t *testing.T) {
	c := plugin.Capability{
		ID: "demo.path", Summary: "s", Safety: plugin.Read,
		Inputs: []plugin.Field{{Name: "out", Type: plugin.Path, Help: "where to write it"}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	prop := InputSchema(c)["properties"].(map[string]any)["out"].(map[string]any)
	if prop["type"] != "string" {
		t.Errorf("type = %v, want string", prop["type"])
	}
	desc, _ := prop["description"].(string)
	if !strings.Contains(desc, "machine running rta") {
		t.Errorf("description = %q, want it to name whose filesystem", desc)
	}
}

// …and one sent anyway — the name is guessable even though the schema hides
// it — must not reach the handler. It is dropped rather than refused, which
// is the one exception to the unknown-argument rule below it: an error
// saying "passphrase" is not accepted confirms to the model that a
// credential input called passphrase exists, which is the disclosure Local
// is there to prevent.
func TestLocalFieldsAreStrippedFromAgentArguments(t *testing.T) {
	s := connect(t, Options{})
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "demo_item_local",
		Arguments: map[string]any{"name": "x", "passphrase": "guessed-by-the-model"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("a guessed credential name was refused instead of dropped: %+v", res.Content)
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if strings.Contains(text, "guessed-by-the-model") {
		t.Fatalf("a caller-supplied Local credential reached the handler: %s", text)
	}
	if !strings.Contains(text, "passphrase=[]") {
		t.Errorf("want an empty passphrase, got: %s", text)
	}
}

// allow issues a grant the way a person at a terminal would, for the store
// this session is using.
func allow(t *testing.T, capID, scope string) {
	t.Helper()
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	err := grant.Save(append(grants, grant.Grant{
		Target: capID, Scope: scope, Issued: time.Now(), Expires: time.Now().Add(time.Hour),
	}))
	if err != nil {
		t.Fatal(err)
	}
}

func callTool(t *testing.T, s *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The allowlist says this agent may in principle delete things. A grant says
// a person allowed this one, now. Passing the first gate is not passing both.
func TestDestructiveCallNeedsAGrant(t *testing.T) {
	s := connect(t, Options{AllowDestructive: []string{"demo.item.rm"}})

	res := callTool(t, s, "demo_item_rm", nil)
	if !res.IsError {
		t.Fatal("a destructive call went through on the allowlist alone")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.grant.required") {
		t.Fatalf("want a grant refusal, got: %s", text)
	}
	// …and it must say what to ask a person for, or the agent just retries.
	if !strings.Contains(text, "rta grant allow demo.item.rm") {
		t.Errorf("refusal carries no usable hint: %s", text)
	}

	allow(t, "demo.item.rm", "")
	if res := callTool(t, s, "demo_item_rm", nil); res.IsError {
		t.Fatalf("granted call still refused: %+v", res.Content)
	}
}

// The wiring this whole feature exists for: a one-time grant authorizes
// exactly one real call over the actual MCP transport, then refuses the
// next — proving Consume is actually reached from the handler, not just
// exercised directly against internal/grant.
func TestOneTimeGrantIsSpentAfterOneRealCall(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := grant.Save(append(grants, grant.Grant{
		Target: "demo.item.reveal", Scope: "staging",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: 1,
	})); verr != nil {
		t.Fatal(verr)
	}

	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}); res.IsError {
		t.Fatalf("the fresh one-time grant was refused: %+v", res.Content)
	}
	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"})
	if !res.IsError {
		t.Fatal("a one-time grant authorized a second call")
	}
	text := res.Content[0].(*sdk.TextContent).Text
	if !strings.Contains(text, "core.grant.required") {
		t.Fatalf("want a grant refusal on the second call, got: %s", text)
	}
}

// The concurrency the whole gate turns on, asserted where the disclosure
// happens rather than where the counter does.
//
// The sequence used to be Check -> Run -> Consume, with only Consume holding
// the lock. The go-sdk dispatches every tools/call in its own goroutine, so
// two pipelined requests both cleared the unlocked Check against MaxUses:1,
// both ran, and both returned the secret — after which the counter read 1 and
// `grant list` showed a grant correctly spent once. Nothing recorded that the
// value had gone out twice.
//
// The pre-existing test for this counted uses in internal/grant, calling
// Consume directly from N goroutines. That pins the arithmetic and cannot
// observe the disclosure: it never calls Check and never runs a handler. This
// counts successful CallTool results, which is the number that matters.
func TestAOneTimeGrantAuthorizesExactlyOneConcurrentCall(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := grant.Save(append(grants, grant.Grant{
		Target: "demo.item.reveal", Scope: "staging",
		Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: 1,
	})); verr != nil {
		t.Fatal(verr)
	}

	const callers = 8
	var wg sync.WaitGroup
	results := make([]bool, callers)
	wg.Add(callers)
	for i := range callers {
		go func() {
			defer wg.Done()
			res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
				Name: "demo_item_reveal", Arguments: map[string]any{"key": "staging"},
			})
			results[i] = err == nil && !res.IsError
		}()
	}
	wg.Wait()

	granted := 0
	for _, ok := range results {
		if ok {
			granted++
		}
	}
	if granted != 1 {
		t.Errorf("a MaxUses:1 grant authorized %d of %d concurrent calls", granted, callers)
	}
}

// A call that passes the grant check but then fails inside Run must not
// spend a one-time grant that revealed or did nothing — the use is refunded.
func TestOneTimeGrantSurvivesAFailedCall(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	grants, verr := grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := grant.Save(append(grants, grant.Grant{
		Target: "demo.item.gatedfail", Issued: time.Now(), Expires: time.Now().Add(time.Hour), MaxUses: 1,
	})); verr != nil {
		t.Fatal(verr)
	}
	if res := callTool(t, s, "demo_item_gatedfail", nil); !res.IsError {
		t.Fatal("expected demo.item.gatedfail to fail, as declared")
	}
	grants, verr = grant.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(grants) != 1 || grants[0].Uses != 0 {
		t.Fatalf("a failed call spent the grant: %+v", grants)
	}
	// And the grant is still good: a second attempt fails for the same
	// declared reason, not because the grant was already spent.
	res := callTool(t, s, "demo_item_gatedfail", nil)
	text := res.Content[0].(*sdk.TextContent).Text
	if strings.Contains(text, "core.grant.required") {
		t.Fatal("the surviving grant was not honored on a second attempt")
	}
	if !strings.Contains(text, "demo.broken") {
		t.Fatalf("expected the capability's own failure, got: %s", text)
	}
}

// A grant names one record, and covers exactly that one.
func TestGrantNarrowsToOneRecord(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	allow(t, "demo.item.reveal", "staging")

	if res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "staging"}); res.IsError {
		t.Fatalf("the granted record was refused: %+v", res.Content)
	}
	res := callTool(t, s, "demo_item_reveal", map[string]any{"key": "production"})
	if !res.IsError {
		t.Fatal("a grant for one record covered another")
	}
}

// A per-record grant is a promise about exactly that record. It used to be
// enforced against one reading of the call's arguments (internal/grant, which
// treats a bare JSON string as one named record) while the handler acted on
// another (plugin.Request.StringSlice, which used to return nil for a bare
// string — "no keys named", and for a capability shaped like kv.env, "no keys
// named" means every key). A JSON-RPC client is not schema-checked before its
// arguments reach the handler, so a caller sending {"key": "staging"} instead
// of {"key": ["staging"]} defeated the entire per-record half of the consent
// model over the network, ending in disclosure of everything a wider grant
// was never issued for.
func TestScalarScopeCannotWidenAGrantedCall(t *testing.T) {
	s := connect(t, Options{AllowWrite: true})
	allow(t, "demo.item.export", "a")

	// The array form: this is what the grant is supposed to cover.
	res := callTool(t, s, "demo_item_export", map[string]any{"key": []any{"a"}})
	if res.IsError {
		t.Fatalf("the granted record was refused: %+v", res.Content)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(got, `"body":"a"`) {
		t.Errorf("array form = %q, want exactly the granted record", got)
	}

	// The scalar form of the identical request must be read the same way —
	// not as "no keys named", which this capability treats as "every key".
	res = callTool(t, s, "demo_item_export", map[string]any{"key": "a"})
	if res.IsError {
		t.Fatalf("the granted record was refused in scalar form: %+v", res.Content)
	}
	if got := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(got, `"body":"a"`) {
		t.Errorf("scalar form = %q, the gate and the handler disagreed about what it named", got)
	}

	// And a record the grant does not cover is still refused in either shape.
	if res := callTool(t, s, "demo_item_export", map[string]any{"key": "b"}); !res.IsError {
		t.Error("an ungranted record went through in scalar form")
	}
}

// The gate has to be visible in the tool description too: a model that reads
// "requires a grant" asks the operator instead of retrying the call.
func TestGrantRequirementIsDescribed(t *testing.T) {
	s := connect(t, Options{AllowWrite: true, AllowDestructive: []string{"demo.item.rm"}})
	tools := listTools(t, s)

	for _, name := range []string{"demo_item_reveal", "demo_item_rm"} {
		if !strings.Contains(tools[name].Description, "grant") {
			t.Errorf("%s does not mention the grant it needs: %s", name, tools[name].Description)
		}
	}
	if strings.Contains(tools["demo_item_set"].Description, "grant") {
		t.Error("an ordinary write should not claim to need a grant")
	}
}

// A closed set belongs in the schema: a model guessing "PTR" at a field that
// wants "ptr" should not have to spend a round trip finding out.
func TestOptionsBecomeSchemaEnums(t *testing.T) {
	tools := listTools(t, connect(t, Options{}))
	props := tools["demo_item_choose"].InputSchema.(map[string]any)["properties"].(map[string]any)

	mode := props["mode"].(map[string]any)
	if got := fmt.Sprint(mode["enum"]); got != "[fast slow]" {
		t.Errorf("mode enum = %v", mode["enum"])
	}
	// A list of choices constrains its items, not the array.
	items := props["kinds"].(map[string]any)["items"].(map[string]any)
	if _, ok := items["enum"]; !ok {
		t.Errorf("kinds items carry no enum: %v", items)
	}
	if _, ok := props["kinds"].(map[string]any)["enum"]; ok {
		t.Error("the array itself must not be an enum of its members")
	}
}

// Suggestions are for people. The names of your secrets are worth something
// without their values, and an agent that legitimately needs the list can
// call the capability that returns it and be gated accordingly.
func TestSuggestionsAreNeverOfferedToAgents(t *testing.T) {
	tools := listTools(t, connect(t, Options{}))
	raw, err := json.Marshal(tools["demo_item_choose"])
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"db-password", "prod-token"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("a suggestion leaked into the tool definition: %s", raw)
		}
	}
}
