package grant

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func setup(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

// issue writes one grant directly, which is what a person at a terminal
// would have produced.
func issue(t *testing.T, g Grant) {
	t.Helper()
	grants, verr := Load()
	if verr != nil {
		t.Fatalf("load: %v", verr)
	}
	if g.Expires.IsZero() {
		g.Expires = time.Now().Add(DefaultTTL)
	}
	if verr := Save(append(grants, g)); verr != nil {
		t.Fatalf("save: %v", verr)
	}
}

func declare(id string, safety plugin.Safety, scope string, needs bool) plugin.Capability {
	return plugin.Capability{
		ID: id, Summary: id, Safety: safety, Scope: scope, NeedsGrant: needs,
		Inputs: []plugin.Field{{Name: scope, Type: plugin.String}},
		Run: func(context.Context, plugin.Request) (view.View, error) {
			return view.Text{Body: "ok"}, nil
		},
	}
}

// Reads pass untouched: the gate is for calls that leak or destroy, and a
// gate on everything is a gate people turn off.
func TestReadsNeedNoGrant(t *testing.T) {
	setup(t)
	if verr := Check(declare("todo.list", plugin.Read, "", false), nil); verr != nil {
		t.Errorf("a read was refused: %v", verr)
	}
}

// Destructive is implicit — a plugin should not have to remember to ask.
func TestDestructiveNeedsAGrantWithoutOptingIn(t *testing.T) {
	setup(t)
	c := declare("todo.rm", plugin.Destructive, "id", false)
	verr := Check(c, map[string]any{"id": "7"})
	if verr == nil {
		t.Fatal("a destructive call went through with no grant")
	}
	if verr.Code != "core.grant.required" {
		t.Errorf("code = %s", verr.Code)
	}
	// The refusal has to tell the agent what to ask a person for, or it will
	// simply try again.
	if !strings.Contains(verr.Hint, "rta grant allow todo.rm 7") {
		t.Errorf("hint = %q", verr.Hint)
	}
}

func TestGrantAuthorizesTheRecordItNames(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "db-password"})
	c := declare("kv.get", plugin.Write, "key", true)

	if verr := Check(c, map[string]any{"key": "db-password"}); verr != nil {
		t.Errorf("the granted key was refused: %v", verr)
	}
	// …and only that one. A grant that widened to the whole store on the
	// first read would be worse than no grant, because it would look right.
	if verr := Check(c, map[string]any{"key": "prod-token"}); verr == nil {
		t.Error("a grant for one key authorized another")
	}
}

// A grant for a plugin covers its capabilities; the reverse is not true.
func TestNamespaceGrant(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv"})
	if verr := Check(declare("kv.get", plugin.Write, "key", true), map[string]any{"key": "any"}); verr != nil {
		t.Errorf("a plugin-wide grant did not cover its capability: %v", verr)
	}
	if verr := Check(declare("todo.rm", plugin.Destructive, "id", false), map[string]any{"id": "1"}); verr == nil {
		t.Error("a kv grant authorized a todo call")
	}
}

// Covering is the same question Check asks, turned around: not "does
// anything authorize this call" but "after some grants are gone, does
// anything left still authorize it". grant.revoke depends on this to avoid
// reporting "No active grant" while a wider one still stands.
func TestCovering(t *testing.T) {
	grants := []Grant{{Target: "kv"}, {Target: "todo.rm", Scope: "1"}}

	if g := Covering(grants, "kv.get", ""); g == nil || g.Target != "kv" {
		t.Errorf("Covering(kv.get) = %v, want the namespace grant", g)
	}
	if g := Covering(grants, "todo.rm", "1"); g == nil {
		t.Error("Covering did not find the exact scoped grant")
	}
	if g := Covering(grants, "todo.rm", "2"); g != nil {
		t.Errorf("a grant scoped to record 1 wrongly covered record 2: %v", g)
	}
	if g := Covering(nil, "kv.get", ""); g != nil {
		t.Errorf("Covering(nil) = %v, want nil", g)
	}
}

// Every record a call names needs cover: two thirds of a leak is a leak.
func TestEveryNamedRecordMustBeCovered(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", Scope: "a"})
	c := declare("kv.env", plugin.Write, "key", true)
	c.Inputs = []plugin.Field{{Name: "key", Type: plugin.StringSlice}}

	if verr := Check(c, map[string]any{"key": []string{"a"}}); verr != nil {
		t.Errorf("the granted key was refused: %v", verr)
	}
	verr := Check(c, map[string]any{"key": []string{"a", "b"}})
	if verr == nil {
		t.Fatal("a partly-granted call went through")
	}
	if !strings.Contains(verr.Message, "b") {
		t.Errorf("the refusal should name what is missing: %q", verr.Message)
	}
	// MCP arguments arrive as []any from JSON, not []string.
	if verr := Check(c, map[string]any{"key": []any{"a", "b"}}); verr == nil {
		t.Error("JSON-shaped arguments bypassed the check")
	}
}

// Naming no record is the widest form of a call, so a scoped grant must not
// cover it: "read this key" cannot become "read every key".
func TestScopedGrantDoesNotCoverAScopelessCall(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", Scope: "a"})
	c := declare("kv.env", plugin.Write, "key", true)
	if verr := Check(c, map[string]any{}); verr == nil {
		t.Error("a grant for one key authorized exporting everything")
	}
	issue(t, Grant{Target: "kv.env"})
	if verr := Check(c, map[string]any{}); verr != nil {
		t.Errorf("an unscoped grant did not cover the unscoped call: %v", verr)
	}
}

func TestExpiredGrantsDoNotAuthorize(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", Expires: time.Now().Add(-time.Second)})
	if verr := Check(declare("kv.get", plugin.Write, "key", true), map[string]any{"key": "k"}); verr == nil {
		t.Error("an expired grant still authorized a read")
	}
	// Expired grants are dropped on read, so nothing has to sweep them.
	grants, _ := Load()
	if len(grants) != 0 {
		t.Errorf("expired grant survived a load: %+v", grants)
	}
}

// The file must be readable without unlocking anything, so it must never
// hold anything worth locking up.
func TestGrantFileHoldsNoSecret(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "db-password", Note: "deploying"})
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var got []Grant
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("grants are not plain JSON: %v", err)
	}
	if got[0].Target != "kv.get" || got[0].Scope != "db-password" {
		t.Errorf("round trip = %+v", got[0])
	}
	if filepath.Base(Path()) != "grants.json" {
		t.Errorf("path = %s", Path())
	}
}

func TestNormalizeAcceptsTheFormsPeopleType(t *testing.T) {
	for in, want := range map[string]string{"kv": "kv", "kv.*": "kv", " kv.get ": "kv.get"} {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

// MaxUses:0 is the shape of every grant issued before the field existed —
// this is the backward-compatibility guarantee the whole feature rests on.
func TestZeroMaxUsesIsUnlimitedLikeBefore(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k"})
	c := declare("kv.get", plugin.Write, "key", true)
	for range 5 {
		if verr := Check(c, map[string]any{"key": "k"}); verr != nil {
			t.Fatalf("an unlimited grant stopped authorizing calls: %v", verr)
		}
		if verr := Consume(c, map[string]any{"key": "k"}); verr != nil {
			t.Fatalf("consume: %v", verr)
		}
	}
}

func TestMaxUsesGrantStopsAuthorizingOnceSpent(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 2})
	c := declare("kv.get", plugin.Write, "key", true)
	values := map[string]any{"key": "k"}

	for i := range 2 {
		if verr := Check(c, values); verr != nil {
			t.Fatalf("call %d: refused while uses remained: %v", i+1, verr)
		}
		if verr := Consume(c, values); verr != nil {
			t.Fatalf("call %d: consume: %v", i+1, verr)
		}
	}
	if verr := Check(c, values); verr == nil {
		t.Fatal("a grant with 0 uses left still authorized a call")
	}
}

// A call that never happened must not cost a use — Consume is only ever
// called by the bridge after Run succeeds, but this pins the contract for
// anyone calling it directly: consuming a scope the grant does not cover is
// a no-op, not an error and not a spend against some other grant.
func TestConsumeIgnoresACallItDoesNotCover(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "a", MaxUses: 1})
	c := declare("kv.get", plugin.Write, "key", true)

	if verr := Consume(c, map[string]any{"key": "b"}); verr != nil {
		t.Fatalf("consume: %v", verr)
	}
	grants, _ := Load()
	if len(grants) != 1 || grants[0].Uses != 0 {
		t.Errorf("an uncovered call spent a use: %+v", grants)
	}
}

// The whole point of the lock: two goroutines racing to spend the same
// one-time grant must not both see it unspent. Without acquireLock this
// reliably overspends under -race on a handful of iterations.
func TestConcurrentConsumeDoesNotOverspendAOneTimeGrant(t *testing.T) {
	setup(t)
	const uses = 8
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: uses})
	c := declare("kv.get", plugin.Write, "key", true)
	values := map[string]any{"key": "k"}

	done := make(chan *view.Error, uses*2)
	for range uses * 2 {
		go func() { done <- Consume(c, values) }()
	}
	for range uses * 2 {
		if verr := <-done; verr != nil {
			t.Fatalf("consume: %v", verr)
		}
	}
	grants, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	// Spent exactly to its limit, not past it — Load already drops it once
	// exhausted, so its absence here is itself part of the assertion: a
	// surviving row would mean Uses < MaxUses despite 2x uses worth of calls.
	if len(grants) != 0 {
		t.Errorf("grant survived %d consumes against a %d-use limit: %+v", uses*2, uses, grants)
	}
}

// A lock file left behind by a process that died mid-update must not wedge
// every future grant check forever.
func TestStaleLockIsReclaimed(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 1})

	path := filepath.Join(filepath.Dir(Path()), lockFile)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * lockStale)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}

	release, verr := acquireLock()
	if verr != nil {
		t.Fatalf("a stale lock was not reclaimed: %v", verr)
	}
	release()
}

// Mutate is the only write path grant.allow and grant.revoke have, so the
// lock has to be inside it: both used to read, filter and save the file with
// no lock at all while Reserve held one across its own round trip.
func TestMutateHoldsTheLockWhileItRewritesTheFile(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 1})

	unlock, verr := acquireLock()
	if verr != nil {
		t.Fatal(verr)
	}
	done := make(chan *view.Error, 1)
	go func() {
		done <- Mutate(func([]Grant) ([]Grant, bool) { return nil, true })
	}()

	select {
	case verr := <-done:
		t.Fatalf("Mutate rewrote the grant file while another writer held the lock: %v", verr)
	case <-time.After(200 * time.Millisecond):
	}
	unlock()

	select {
	case verr := <-done:
		if verr != nil {
			t.Fatalf("mutate: %v", verr)
		}
	case <-time.After(lockTimeout):
		t.Fatal("Mutate never finished after the lock was released")
	}
	if grants, _ := Load(); len(grants) != 0 {
		t.Errorf("the mutation did not land: %+v", grants)
	}
}

// The mutator is handed loadAll's answer, not Load's. A grant spent to its
// last use is not Active, so Load hides it — and a writer that round-trips
// through Load deletes it merely by saving, taking with it the row refund
// needs to give the use back after a failed call.
func TestMutateSeesTheGrantsLoadHides(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 1, Uses: 1})
	if active, _ := Load(); len(active) != 0 {
		t.Fatalf("the fixture is not actually hidden from Load: %+v", active)
	}

	seen := 0
	if verr := Mutate(func(g []Grant) ([]Grant, bool) {
		seen = len(g)
		return g, true
	}); verr != nil {
		t.Fatal(verr)
	}
	if seen != 1 {
		t.Errorf("the mutator saw %d grants, want the spent one too", seen)
	}
	stored, verr := loadAll()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(stored) != 1 {
		t.Errorf("saving what the mutator returned dropped the spent grant a refund still needs: %+v", stored)
	}
}

// Declining is how --dry-run and "there was nothing to revoke" get their
// answer: the decision is taken under the same lock as the write it refuses,
// so the file cannot change between deciding and reporting.
func TestMutateStoresNothingWhenTheMutatorDeclines(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k"})

	if verr := Mutate(func([]Grant) ([]Grant, bool) { return nil, false }); verr != nil {
		t.Fatal(verr)
	}
	grants, _ := Load()
	if len(grants) != 1 || grants[0].Target != "kv.get" {
		t.Errorf("a declined mutation wrote anyway: %+v", grants)
	}
}
