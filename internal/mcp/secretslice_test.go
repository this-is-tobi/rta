package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

// The sink this whole change exists for.
//
// auditArgs writes into internal/agentlog, which is sealed, hash-chained and
// designed for long retention — docs/30-boundary/40-audit-trail.md recommends shipping it
// somewhere durable on a cron line, and its own comment says it is "read by
// people and by the next agent that greps it". It masked a value only when
// its field was declared Secret, and a repeatable credential could not be
// declared Secret because no such type existed. So `vault.kv.set --data
// 'password=hunter2'` — the operation of writing a secret into a secret
// manager — wrote the secret into the audit log in cleartext, on a record
// nobody can redact afterwards without visibly breaking the chain.
func TestARepeatableCredentialIsMaskedInTheAuditLog(t *testing.T) {
	c := plugin.Capability{
		ID: "vault.kv.set", Summary: "s", Safety: plugin.Write,
		Inputs: []plugin.Field{
			{Name: "path", Type: plugin.String, Required: true, Help: "h"},
			{Name: "data", Type: plugin.SecretSlice, Required: true, Help: "h"},
			{Name: "token", Type: plugin.Secret, Help: "h"},
		},
	}
	got := auditArgs(c, map[string]any{
		"path":  "apps/prod",
		"data":  []string{"password=hunter2", "api_key=sk-live-abcdef"},
		"token": "s.rootToken",
	})

	if got["data"] != view.Mask {
		t.Errorf("the repeatable credential was not masked: %#v", got["data"])
	}
	if got["token"] != view.Mask {
		t.Errorf("the scalar credential was not masked: %#v", got["token"])
	}
	// The record still has to be worth keeping: what was acted on stays
	// legible, which is the reason this is a field-type decision and not a
	// scrubber that guesses by looking at values.
	if got["path"] != "apps/prod" {
		t.Errorf("the record lost the thing it was about: %#v", got["path"])
	}

	// Nothing anywhere in the entry, not just the one key.
	if flat := strings.Join([]string{
		toS(got["path"]), toS(got["data"]), toS(got["token"]),
	}, " "); strings.Contains(flat, "hunter2") || strings.Contains(flat, "sk-live") {
		t.Errorf("a credential survived somewhere in the audited arguments: %s", flat)
	}
}

func toS(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []string:
		return strings.Join(t, ",")
	default:
		return ""
	}
}
