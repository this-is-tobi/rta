package grant

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// connected is a capability a profile can fill, which is what lets a call
// name one (plugin.Profilable).
func connected(id string, safety plugin.Safety) plugin.Capability {
	return plugin.Capability{ID: id, Summary: id, Safety: safety,
		Inputs: []plugin.Field{{Name: "host", Type: plugin.String, Default: "localhost", Config: "host", Local: true}}}
}

// refusal is the gate's answer to a call nothing covers, as by makes it.
func refusal(t *testing.T, c plugin.Capability, values map[string]any, by Caller) *view.Error {
	t.Helper()
	release, verr := Reserve(c, values, by)
	if verr == nil {
		release()
		t.Fatalf("%s was let through with no grant", c.ID)
	}
	return verr
}

// A read refused over a profile hands on the grant for that read. It handed on
// the one for its plugin, so the operator who pasted what an agent relayed for
// prom.overview had let prom.series.rm through as well.
func TestARefusalOverAProfileNamesTheCapabilityItRefused(t *testing.T) {
	setup(t)
	verr := refusal(t, connected("prom.overview", plugin.Read), map[string]any{}, Caller{Profile: "scratch", Pin: "p"})
	if verr.Code != "core.grant.required" {
		t.Fatalf("code = %q, want core.grant.required", verr.Code)
	}
	if !strings.Contains(verr.Hint, "`rta grant allow prom.overview --profile scratch --ttl 15m`") {
		t.Errorf("the refusal does not hand on the grant for the call it refused:\n%s", verr.Hint)
	}
	if strings.Contains(verr.Hint, "grant allow prom ") {
		t.Errorf("the gate hands on a grant on the whole plugin:\n%s", verr.Hint)
	}
}

// The grant on the whole plugin comes after the call's own, with what it
// covers counted as the receipt counts it: before it is issued, where the
// receipt says so only once it stands.
func TestThePluginIsOfferedSecondWithWhatItCovers(t *testing.T) {
	setup(t)
	humanOnly := connected("prom.wipe", plugin.Destructive)
	humanOnly.HumanOnly = true
	catalog := []plugin.Capability{
		connected("prom.overview", plugin.Read),
		connected("prom.query", plugin.Read),
		connected("prom.reload", plugin.Write),
		connected("prom.series.rm", plugin.Destructive),
		// Neither is a call an agent can make over the profile: one takes no
		// profile, the other is for a person at a terminal.
		{ID: "prom.version", Summary: "v", Safety: plugin.Read},
		humanOnly,
		connected("other.drop", plugin.Destructive),
	}
	c := catalog[0]
	by := Caller{Profile: "scratch", Pin: "p", Agent: "auditbot"}
	verr := WithPluginWide(refusal(t, c, map[string]any{}, by), c, map[string]any{}, catalog, by)

	narrow := strings.Index(verr.Hint, "`rta grant allow prom.overview --profile scratch --agent auditbot --ttl 15m`")
	wide := strings.Index(verr.Hint, "`rta grant allow prom --profile scratch --agent auditbot --ttl 15m`")
	if narrow < 0 || wide < 0 || wide < narrow {
		t.Fatalf("the call's own grant is not handed on first and the plugin's second:\n%s", verr.Hint)
	}
	if !strings.Contains(verr.Hint[wide:], "which covers 4 capabilities an agent needs a grant for, 1 destructive (prom.series.rm)") {
		t.Errorf("the plugin is offered without what it covers:\n%s", verr.Hint)
	}
	if verr.Code != "core.grant.required" || !strings.HasPrefix(verr.Message, "no active grant for prom_overview") {
		t.Errorf("the offer changed the refusal itself: %v", verr)
	}
}

// Offered one step from the call and nowhere else: a call with no profile, a
// call naming a record and a plugin with nothing more to reach over the
// profile are refused as the gate refused them.
func TestThePluginIsOfferedOnlyOneStepFromTheCall(t *testing.T) {
	setup(t)
	read := connected("vault.kv.get", plugin.Read)
	read.Scope, read.Reveals = "path", true
	read.Inputs = append(read.Inputs, plugin.Field{Name: "path", Type: plugin.String})
	catalog := []plugin.Capability{
		read,
		connected("vault.overview", plugin.Read),
		connected("vault.kv.delete", plugin.Destructive),
		connected("lone.status", plugin.Read),
	}
	for _, tc := range []struct {
		name   string
		c      plugin.Capability
		values map[string]any
		by     Caller
	}{
		{"no profile", catalog[2], map[string]any{}, Caller{}},
		{"a call naming a record", read, map[string]any{"path": "secret/db"}, Caller{Profile: "prod", Pin: "p"}},
		{"nothing more on the profile", catalog[3], map[string]any{}, Caller{Profile: "prod", Pin: "p"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := refusal(t, tc.c, tc.values, tc.by)
			if after := WithPluginWide(before, tc.c, tc.values, catalog, tc.by); after.Hint != before.Hint {
				t.Errorf("a wider grant was offered:\n%s", after.Hint)
			}
		})
	}
	paced := view.Errorf("core.grant.rate", "spent").WithHint("wait 1m")
	if got := WithPluginWide(paced, catalog[1], map[string]any{}, catalog, Caller{Profile: "prod"}); got.Hint != "wait 1m" {
		t.Errorf("a refusal that is not for a missing grant was given an offer: %q", got.Hint)
	}
}

func TestTheDestructiveSummaryNeverSaysAndOneOther(t *testing.T) {
	for _, c := range []struct {
		ids  []string
		want string
	}{
		{nil, "none destructive"},
		{[]string{"a.rm"}, "1 destructive (a.rm)"},
		{[]string{"a", "b", "c"}, "3 destructive (a, b, c)"},
		{[]string{"a", "b", "c", "d"}, "4 destructive (a, b, c, d)"},
		{[]string{"a", "b", "c", "d", "e"}, "5 destructive (a, b, c and 2 others)"},
	} {
		if got := DestructiveSummary(c.ids); got != c.want {
			t.Errorf("DestructiveSummary(%v) = %q, want %q", c.ids, got, c.want)
		}
	}
}
