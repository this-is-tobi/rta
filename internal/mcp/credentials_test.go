package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// fetcher stands for a capability taking a URL and headers, as the http ones
// do: neither is declared a Secret, since neither is one by its type, and each
// may carry a credential by its shape.
func fetcher(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	err := reg.Register(plugin.Plugin{
		Name: "web", Summary: "web", Capabilities: []plugin.Capability{{
			ID: "web.get", Summary: "fetch a URL", Safety: plugin.Read, NeedsGrant: true, Scope: "url",
			Inputs: []plugin.Field{
				{Name: "url", Type: plugin.String, Required: true, Help: "the URL"},
				{Name: "header", Type: plugin.StringSlice, Help: "headers"},
			},
			Run: func(_ context.Context, req plugin.Request) (view.View, error) {
				return nil, errors.New("Get \"" + req.String("url") + "\": connection refused")
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A call that needs a grant and names a URL with a token in it was handed the
// command to issue one, masked like everything else the agent is handed: a
// command for a URL nobody sent, which issues a grant that covers nothing. A
// command that does not fix the problem is worse than none, so the hint says
// what does: keep the credential out of the record.
func TestNoCommandIsHandedOverForARecordThatCarriesACredential(t *testing.T) {
	s := connectWithData(t, fetcher(t), Options{})

	res := callTool(t, s, "web_get", map[string]any{"url": "https://api.example.com/v1?token=tok123"})
	text := res.Content[0].(*sdk.TextContent).Text
	if !res.IsError || !strings.Contains(text, "core.grant.required") {
		t.Fatalf("an ungranted call was not refused as such: %s", text)
	}
	if strings.Contains(text, "grant allow") || strings.Contains(text, "tok123") {
		t.Errorf("the refusal hands over a command that cannot work, or the credential: %s", text)
	}
	if !strings.Contains(text, "not in the record") {
		t.Errorf("the refusal does not say what to do instead: %s", text)
	}

	res = callTool(t, s, "web_get", map[string]any{"url": "https://api.example.com/v1?page=2"})
	if text := res.Content[0].(*sdk.TextContent).Text; !strings.Contains(text, "grant allow web.get") {
		t.Errorf("a record with no credential lost its command: %s", text)
	}
}

// The agent log is sealed, permanent and meant to be read, and what an agent
// put in a URL or a header was written into it as it was sent: the userinfo of
// a URL, a token in its query, an Authorization header among the others. Only a
// field declared a Secret was masked, and a URL is not one by its type. The
// host and path stay, since they are what the record is for, and so does the
// rest of every header.
func TestACredentialInAURLOrAHeaderIsNotWrittenToTheRecord(t *testing.T) {
	s := connectWithData(t, fetcher(t), Options{})
	allow(t, "web.get", "")

	res := callTool(t, s, "web_get", map[string]any{
		"url":    "https://alice:s3cret@api.example.com/v1?token=tok123&page=2",
		"header": []string{"Accept: application/json", "Authorization: Bearer hunter2"},
	})
	if !res.IsError {
		t.Fatal("the fixture's call was expected to fail")
	}
	if text := res.Content[0].(*sdk.TextContent).Text; strings.Contains(text, "s3cret") || strings.Contains(text, "tok123") {
		t.Errorf("the error the agent is handed carries the credential: %s", text)
	}

	rows, err := agentlog.Read(1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("the record: %v, %v", rows, err)
	}
	raw, _ := json.Marshal(rows[0])
	line := string(raw)
	for _, leaked := range []string{"s3cret", "tok123", "hunter2", "alice"} {
		if strings.Contains(line, leaked) {
			t.Errorf("the record holds %q: %s", leaked, line)
		}
	}
	for _, kept := range []string{"api.example.com/v1", "page=2", "Accept: application/json"} {
		if !strings.Contains(line, kept) {
			t.Errorf("the record lost %q, which is not a credential: %s", kept, line)
		}
	}
}
