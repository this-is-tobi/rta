package mcp

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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

// The http capabilities' header and url inputs are plain, so what the record
// keeps of them is what textclean.Credentials leaves; the http dry run and the
// completion history decide the same question by credname. A header or a
// parameter that list calls a credential was masked in the dry run, forgotten
// by the history, and written in clear into the sealed record.
func TestTheRecordMasksEveryCredentialTheDryRunMasks(t *testing.T) {
	c := plugin.Capability{Inputs: []plugin.Field{
		{Name: "header", Type: plugin.StringSlice},
		{Name: "url", Type: plugin.String},
	}}
	const secret = "glpat-REALLYSECRET0123"
	for _, h := range []string{"PRIVATE-TOKEN", "X-Vault-Token", "X-Goog-Api-Key", "Api-Key", "X-Auth-Key", "Authorization", "X-Api-Key"} {
		got := auditArgs(c, map[string]any{"header": []string{h + ": " + secret}})
		if kept := got["header"]; strings.Contains(toString(kept), secret) {
			t.Errorf("the record keeps %s in clear: %v", h, kept)
		}
	}
	for _, p := range []string{"private_token", "apiKey", "x-vault-token", "session", "token", "api_token"} {
		got := auditArgs(c, map[string]any{"url": "https://gitlab.example/api/v4/projects?" + p + "=" + secret})
		if kept := got["url"]; strings.Contains(toString(kept), secret) {
			t.Errorf("the record keeps ?%s= in clear: %v", p, kept)
		}
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []string:
		return strings.Join(x, "\n")
	case []any:
		var b strings.Builder
		for _, e := range x {
			if s, ok := e.(string); ok {
				b.WriteString(s + "\n")
			}
		}
		return b.String()
	}
	return ""
}
