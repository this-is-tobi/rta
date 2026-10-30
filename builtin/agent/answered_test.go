package agent

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/view"
)

func overviewPairs(t *testing.T) map[string]string {
	t.Helper()
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range v.(view.KeyValue).Pairs {
		got[p.Key] = p.Value
	}
	return got
}

func appendAll(t *testing.T, entries ...agentlog.Entry) {
	t.Helper()
	for _, e := range entries {
		if err := agentlog.Append(e); err != nil {
			t.Fatal(err)
		}
	}
}

func refusedForAGrant(capID string, records ...string) agentlog.Entry {
	return agentlog.Entry{Cap: capID, Agent: "claude", Records: records, Outcome: agentlog.Refused,
		Auth: agentlog.Blocked, Code: "core.grant.required"}
}

// A refusal for want of a grant stops asking once the operator has answered
// it: the agent's retry of the same call got past the gate, whatever it did
// then, or a grant stands that covers it. The tile went on saying "2 need
// grant" after both, beside the grant list holding the grants.
func TestARefusalTheOperatorAnsweredIsNoLongerPending(t *testing.T) {
	isolate(t)
	appendAll(t,
		refusedForAGrant("note.add"),
		refusedForAGrant("kv.get", "db/password"),
		agentlog.Entry{Cap: "note.add", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Standing},
	)
	now := time.Now()
	if verr := grant.Issue(grant.Grant{Target: "kv.get", Agent: "claude", Scope: "db/password",
		Issued: now, Expires: now.Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	got := overviewPairs(t)
	if want := "0 · 2 answered"; got["needs your grant"] != want {
		t.Fatalf("needs your grant = %q, want %q", got["needs your grant"], want)
	}
	if status := got["status"]; !strings.HasPrefix(status, "ok") {
		t.Fatalf("status = %q: an answered refusal is no warning", status)
	}
}

// What has not been answered stays pending: a retry refused for the same want,
// a retry of a different call, and a grant for a different record.
func TestARefusalNothingAnsweredStaysPending(t *testing.T) {
	isolate(t)
	appendAll(t,
		refusedForAGrant("note.add"),
		refusedForAGrant("note.add"),
		refusedForAGrant("kv.get", "db/password"),
		agentlog.Entry{Cap: "kv.get", Agent: "claude", Records: []string{"db/other"}, Outcome: agentlog.Ran,
			Auth: agentlog.Standing},
	)
	now := time.Now()
	if verr := grant.Issue(grant.Grant{Target: "kv.get", Agent: "claude", Scope: "db/other",
		Issued: now, Expires: now.Add(time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	got := overviewPairs(t)
	if want := "pending 3"; !strings.HasPrefix(got["needs your grant"], want) {
		t.Fatalf("needs your grant = %q, want it to begin %q", got["needs your grant"], want)
	}
}
