package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// hintOf is the hint of a refusal, decoded the way messageOf decodes the
// sentence.
func hintOf(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	var e struct {
		Hint string `json:"hint"`
	}
	if err := json.Unmarshal([]byte(contentText(t, res)), &e); err != nil {
		t.Fatalf("not a refusal envelope: %v", err)
	}
	return e.Hint
}

// The agent knows a tool by one name, the one it called, and every sentence
// this server says to it spells that name: the input errors already did, and
// the host's own refusals named the capability by its dotted ID instead, which
// nothing on its side of the wire is called. A command in a hint is the
// operator's, typed at a terminal, and keeps the ID the terminal knows it by.
func TestAHostRefusalNamesTheToolTheAgentCalled(t *testing.T) {
	f := newProfileFixture(t, twoProfiles)

	t.Run("a call with no grant", func(t *testing.T) {
		res := f.callGated(t)
		assertCode(t, res, "core.grant.required")
		if got := messageOf(t, res); got != "no active grant for sys_gated" {
			t.Errorf("the refusal says %q, want it to name the sys_gated tool", got)
		}
		if hint := hintOf(t, res); !strings.Contains(hint, "rta grant allow sys.gated") {
			t.Errorf("the hint no longer carries the operator's command: %q", hint)
		}
	})

	t.Run("a call that names no profile where one is needed", func(t *testing.T) {
		res := f.call(t, map[string]any{"sql": "select 1"})
		assertCode(t, res, "core.profile.required")
		if got := messageOf(t, res); !strings.HasPrefix(got, "pg_query has configured connections") {
			t.Errorf("the refusal says %q, want it to name the pg_query tool", got)
		}
	})

	t.Run("a call outside the fence of its connection", func(t *testing.T) {
		fence := newFenceFixture(t, fenced, t.TempDir())
		fence.grantFor(t, "staging", 0)
		res := fence.call(t, map[string]any{"profile": "staging", "namespace": "prod"})
		assertCode(t, res, "core.profile.fenced")
		if got := messageOf(t, res); !strings.HasPrefix(got, "app_status was not run:") {
			t.Errorf("the refusal says %q, want it to name the app_status tool", got)
		}
		res = fence.call(t, map[string]any{"profile": "staging", "all-namespaces": true})
		assertCode(t, res, "core.profile.fenced.switch")
		if got := messageOf(t, res); !strings.HasPrefix(got, "app_status was not run:") {
			t.Errorf("the switch refusal says %q, want it to name the app_status tool", got)
		}
	})
}
