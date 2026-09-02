package grant

import (
	"testing"
	"time"
)

// Instance refs stay byte-exact where consent is compared: a grant for the
// analytics database does not authorize the default one, in either direction.
func TestInstanceRefsDoNotCrossMatch(t *testing.T) {
	g := Grant{Target: "pg.query", Profile: "staging/analytics"}
	if g.covers("pg.query", "", Caller{Profile: "staging"}) {
		t.Error("an analytics grant covered the default instance")
	}
	if g.covers("pg.query", "", Caller{Profile: "staging/analytics"}) == false {
		t.Error("the exact ref stopped matching itself")
	}
	def := Grant{Target: "pg.query", Profile: "staging"}
	if def.covers("pg.query", "", Caller{Profile: "staging/analytics"}) {
		t.Error("a default-instance grant covered analytics")
	}
}

// The active-environment bound compares by the name half: switching staging
// on keeps a grant for staging/analytics reachable — the instance is inside
// the place the operator switched on — while any other environment's grants
// still drop.
func TestActiveBoundKeepsInstanceGrantsInsideTheEnvironment(t *testing.T) {
	g := Grant{Target: "pg.query", Profile: "staging/analytics"}
	if !callerReachable(g, Caller{Active: "staging"}) {
		t.Error("activating staging dropped its own analytics grant")
	}
	if callerReachable(g, Caller{Active: "prod"}) {
		t.Error("activating prod kept a staging instance grant reachable")
	}
}

// Deleting an environment revokes its instance grants too: a grant naming
// staging/analytics must not outlive staging as a row that reads like access.
func TestRevokeProfileTakesInstanceGrants(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	now := time.Now()
	if verr := Save([]Grant{
		{Target: "pg.query", Profile: "staging", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg.query", Profile: "staging/analytics", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg.query", Profile: "prod/main", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}
	if n := RevokeProfile("staging", now); n != 2 {
		t.Errorf("revoked %d grants, want both staging ones", n)
	}
	left, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(left) != 1 || left[0].Profile != "prod/main" {
		t.Errorf("grants left = %+v, want only prod/main", left)
	}
}
