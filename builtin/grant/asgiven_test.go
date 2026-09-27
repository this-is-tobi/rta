package grant

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"

	core "github.com/this-is-tobi/rta/internal/grant"
	operatorid "github.com/this-is-tobi/rta/internal/operator"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// scopesOn is every standing grant's record on target, sorted.
func scopesOn(t *testing.T, target string) []string {
	t.Helper()
	var out []string
	for _, g := range standing(t) {
		if g.Target == target {
			out = append(out, g.Scope)
		}
	}
	slices.Sort(out)
	return out
}

// A record is taken as it was typed, the way the gate judges it. allow,
// renew and revoke trimmed theirs, no-break space included: the command a
// core.grant.required refusal offers for a padded call issued a grant on the
// bare record, which covers nothing that call asked for, and a revoke naming
// the padded record took back the bare grant and left the padded one.
func TestAllowRenewAndRevokeTakeTheRecordAsGiven(t *testing.T) {
	setup(t)
	padded := "db-password" + string(rune(0xa0))
	run(t, allowH, map[string]any{"target": "kv.get", "scope": padded, "ttl": "15m"})
	if got := scopesOn(t, "kv.get"); !slices.Equal(got, []string{padded}) {
		t.Fatalf("allow on the padded record stored %q, want it as given", got)
	}
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "ttl": "15m"})
	if got := scopesOn(t, "kv.get"); !slices.Equal(got, []string{"db-password", padded}) {
		t.Fatalf("the bare record replaced the padded one: %q", got)
	}

	v, err := renew(t, map[string]any{"target": "kv.get", "scope": padded, "ttl": "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if body := v.(view.Text).Body; !strings.HasPrefix(body, "renewed 1 grant") || !strings.Contains(body, strconv.QuoteToASCII(padded)) {
		t.Errorf("renew on the padded record said %q, want that one grant and no other", body)
	}
	for _, g := range standing(t) {
		if g.Scope == "db-password" && g.TTL != "15m" {
			t.Errorf("renew on the padded record renewed the bare one, to %q", g.TTL)
		}
	}

	run(t, runRevoke, map[string]any{"target": "kv.get", "scope": padded})
	if got := scopesOn(t, "kv.get"); !slices.Equal(got, []string{"db-password"}) {
		t.Errorf("revoke on the padded record left %q, want the bare grant alone", got)
	}
}

// A record that is only white space is refused, by every verb that takes
// one. Trimmed, it was the empty record, which is every record: allow issued
// a grant over the whole store and revoke took back every grant on it.
func TestARecordOfWhiteSpaceAloneIsRefused(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password"})
	for _, blank := range []string{" ", "\t", string(rune(0xa0)), "  "} {
		for name, verb := range map[string]func() error{
			"allow": func() error {
				_, err := allowH(context.Background(), req(map[string]any{"target": "kv.get", "scope": blank}))
				return err
			},
			"renew": func() error {
				_, err := renew(t, map[string]any{"target": "kv.get", "scope": blank})
				return err
			},
			"revoke": func() error {
				_, err := runRevoke(context.Background(), req(map[string]any{"target": "kv.get", "scope": blank}))
				return err
			},
		} {
			err := verb()
			if err == nil {
				t.Errorf("%s on %q was taken", name, blank)
				continue
			}
			if got := code(t, err); got != "grant.scope.blank" {
				t.Errorf("%s on %q: %v, want grant.scope.blank", name, blank, err)
			}
		}
	}
	if got := scopesOn(t, "kv.get"); !slices.Equal(got, []string{"db-password"}) {
		t.Errorf("the refusals changed the roster to %q", got)
	}
	if _, verr := RevokeRemote(operatorid.RevokeSpec{Target: "kv.get", Scope: " "}, true); verr == nil || verr.Code != "grant.scope.blank" {
		t.Errorf("the operator channel's revoke on white space: %v, want grant.scope.blank", verr)
	}
	if got := scopesOn(t, "kv.get"); !slices.Equal(got, []string{"db-password"}) {
		t.Errorf("the operator channel's revoke changed the roster to %q", got)
	}
}

// The operator channel's review holds a server's draft to the record asked
// for, byte for byte, as the builder now takes it: it compared the trimmed
// record, so a server that dropped the padding got a grant on another record
// signed.
func TestAPreparedDraftKeepsTheRecordAsAsked(t *testing.T) {
	padded := "prod/db" + string(rune(0xa0))
	spec := operatorid.IssueSpec{Target: "kv.get", Scope: padded, Agent: "lab-agent"}
	draft := core.Grant{Target: "kv.get", Scope: "prod/db", Agent: "lab-agent", Server: "https://lab"}
	verr := checkPrepared(plugin.SurfaceCLI, spec, "https://lab", draft)
	if verr == nil || verr.Code != "core.operator.prepare.mismatch" || !strings.Contains(verr.Message, "record scope") {
		t.Fatalf("a draft on the trimmed record: %v, want core.operator.prepare.mismatch on the record", verr)
	}
	if !strings.Contains(verr.Message, strconv.QuoteToASCII(padded)) {
		t.Errorf("the refusal reads %q, want the record asked for shown as the gate compares it", verr.Message)
	}
}
