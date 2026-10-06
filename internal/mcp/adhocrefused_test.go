package mcp

import (
	"context"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/toolcall"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A connection stated on a command line (--kube, --secret, --secrets-from) is a
// person's act on their own machine, and nothing an agent can name: over MCP a
// connection is a profile, which a person consented to. The flags exist on no
// tool, and a call that sends their names is refused as it would refuse any
// name the tool does not have.

func TestTheConnectionFlagsAreOnNoToolAnAgentIsOffered(t *testing.T) {
	c := plugin.Capability{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true,
				Endpoint: plugin.EndpointHost, Help: "host"},
			{Name: "port", Type: plugin.Int, Default: 5432, Config: "port", Local: true,
				Endpoint: plugin.EndpointPort, Help: "port"},
			{Name: "password", Type: plugin.Secret, Local: true, EnvFallback: true, Help: "password"},
		},
	}
	props, _ := toolcall.InputSchema(c, nil, nil)["properties"].(map[string]any)
	for _, name := range []string{"kube", "secret", "secrets-from", "ssh"} {
		if _, offered := props[name]; offered {
			t.Errorf("the tool offers %q, which is a flag of a person's command line", name)
		}
	}
}

func TestACallThatSendsAConnectionFlagIsRefusedAsAnyNameTheToolDoesNotHave(t *testing.T) {
	s := connect(t, Options{})
	for _, name := range []string{"kube", "secret", "secrets-from"} {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "demo_item_local",
			Arguments: map[string]any{"name": "x", name: "prod/databases/svc/postgres:5432"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError {
			t.Fatalf("%q was taken by a tool that does not offer it: %+v", name, res.Content)
		}
		text := res.Content[0].(*sdk.TextContent).Text
		if !strings.Contains(text, "core.mcp.badargs") || strings.Contains(text, "databases/svc/postgres") {
			t.Errorf("%q was refused as %s", name, text)
		}
	}
}
