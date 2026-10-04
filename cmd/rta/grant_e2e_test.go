//go:build unix

package main_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

// records is every standing grant's Record cell, as `grant list -o json`
// hands a script the roster.
func records(t *testing.T) []string {
	t.Helper()
	env := envelope(t, run(t, "grant", "list", "-o", "json"))
	cols, _ := env["columns"].([]any)
	at := -1
	for i, c := range cols {
		if m, _ := c.(map[string]any); m["name"] == "Record" {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no Record column in %v", env["columns"])
	}
	var out []string
	rows, _ := env["rows"].([]any)
	for _, r := range rows {
		row, _ := r.([]any)
		out = append(out, row[at].(string))
	}
	slices.Sort(out)
	return out
}

// A grant is issued on, and taken back from, the record the command names,
// as the shell hands it over. allow and revoke trimmed it, no-break space
// included, so the command a core.grant.required refusal offers for a call
// on a padded record — shell-quoted with the padding — issued a grant on
// the bare record, which covers nothing that call asked for, and a revoke
// naming the padded record took the bare grant back instead.
func TestAGrantIsIssuedAndRevokedOnTheRecordTyped(t *testing.T) {
	padded := "prod/db" + string(rune(0xa0))
	for _, record := range []string{padded, "prod/db"} {
		if r := run(t, "grant", "allow", "kv.get", record, "--ttl", "15m", "--agent", "probe"); r.code != 0 {
			t.Fatalf("allow %q: exit %d, %s", record, r.code, r.stderr)
		}
	}
	shown := strconv.QuoteToASCII(padded)
	if got := records(t); !slices.Equal(got, []string{shown, "prod/db"}) {
		t.Fatalf("the roster holds %q, want the padded record beside the bare one", got)
	}

	if r := run(t, "grant", "revoke", "kv.get", padded); r.code != 0 {
		t.Fatalf("revoke: exit %d, %s", r.code, r.stderr)
	}
	if got := records(t); !slices.Equal(got, []string{"prod/db"}) {
		t.Fatalf("revoking the padded record left %q, want the bare grant alone", got)
	}

	// White space alone names no record. Trimmed, it was every record.
	for _, args := range [][]string{
		{"grant", "allow", "kv.get", " ", "--agent", "probe"},
		{"grant", "renew", "kv.get", " "},
		{"grant", "revoke", "kv.get", " "},
	} {
		r := run(t, args...)
		if r.code != 1 || !strings.Contains(r.stderr, "grant.scope.blank") {
			t.Errorf("%q: exit %d, stderr %q; want 1 and grant.scope.blank", args, r.code, r.stderr)
		}
	}
	if got := records(t); !slices.Equal(got, []string{"prod/db"}) {
		t.Errorf("the refusals left %q, want the roster untouched", got)
	}
}
