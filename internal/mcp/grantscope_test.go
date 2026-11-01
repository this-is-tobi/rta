package mcp

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/operator"
)

// The grant file is every server's of this account, and a roster is one
// server's trust decision, the reason the lock verbs answer for what their
// server owns (lockscope_test.go). grant.list handed an operator enrolled
// only on staging the whole file: prod's agent, its records and its profiles.
// It lists the grants naming staging's own agent, the only ones that
// authorize anything there.
func TestAnOperatorOfOneServerReadsItsOwnAgentsGrants(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	signer, roster := enrolled(t, "contractor")
	now := time.Now()
	for _, g := range []grant.Grant{
		{Target: "demo.item.reveal", Agent: "staging", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "demo.item.reveal", Scope: "payments", Agent: "prod", Issued: now, Expires: now.Add(time.Hour)},
	} {
		if verr := grant.Issue(g, true); verr != nil {
			t.Fatal(verr)
		}
	}
	addr := startOperatorWith(t, OperatorConfig{Roster: roster, Agent: "staging"})
	status, body := postEnvelope(t, addr, signer.Sign("http://"+addr, fetchChallenge(t, addr), operator.VerbGrantList, nil))
	if status != http.StatusOK {
		t.Fatalf("grant.list: %d — %s", status, body)
	}
	var gl operator.GrantList
	if err := json.Unmarshal(body, &gl); err != nil {
		t.Fatal(err)
	}
	if len(gl.Grants) != 1 || gl.Grants[0].Agent != "staging" {
		t.Fatalf("staging's operator read %+v, want staging's grant alone", gl.Grants)
	}
}
