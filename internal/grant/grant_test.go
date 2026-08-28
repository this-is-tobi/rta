package grant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/internal/paths"
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
	// Both stamps, because both are set by the only thing that issues a grant
	// for real (builtin/grant's runAllow). Issued in particular: Active caps
	// a grant at Issued+MaxTTL, so a fixture that leaves it zero is a grant
	// that expired in the year 1 and tests a rule nobody wrote.
	if g.Issued.IsZero() {
		g.Issued = time.Now()
	}
	if g.Expires.IsZero() {
		g.Expires = time.Now().Add(DefaultTTL)
	}
	if verr := Save(append(grants, g)); verr != nil {
		t.Fatalf("save: %v", verr)
	}
}

// gate asks whether a call is authorized, without keeping the use — Reserve
// spends under the lock, and release() gives it back, which is exactly what
// the bridge does when the call it authorized then fails.
//
// These tests used to call an exported Check() that decided without spending.
// Nothing in the product ever called it, so every assertion about scope
// matching, secret gating and budget exhaustion was made against a function no
// call ever went through. Going through Reserve costs a spend-and-refund round
// trip and buys the property the suite is supposed to have: the thing being
// tested is the thing that runs.
func gate(t *testing.T, c plugin.Capability, values map[string]any, profile, pin string) *view.Error {
	t.Helper()
	release, verr := Reserve(c, values, profile, pin, "")
	if verr == nil {
		release()
	}
	return verr
}

// call is an authorized call that then succeeds: the use stays spent, because
// the bridge only calls release() on failure.
func call(t *testing.T, c plugin.Capability, values map[string]any, profile, pin string) *view.Error {
	t.Helper()
	_, verr := Reserve(c, values, profile, pin, "")
	return verr
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
	if verr := gate(t, declare("todo.list", plugin.Read, "", false), nil, "", ""); verr != nil {
		t.Errorf("a read was refused: %v", verr)
	}
}

// Destructive is implicit — a plugin should not have to remember to ask.
func TestDestructiveNeedsAGrantWithoutOptingIn(t *testing.T) {
	setup(t)
	c := declare("todo.rm", plugin.Destructive, "id", false)
	verr := gate(t, c, map[string]any{"id": "7"}, "", "")
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

	if verr := gate(t, c, map[string]any{"key": "db-password"}, "", ""); verr != nil {
		t.Errorf("the granted key was refused: %v", verr)
	}
	// …and only that one. A grant that widened to the whole store on the
	// first read would be worse than no grant, because it would look right.
	if verr := gate(t, c, map[string]any{"key": "prod-token"}, "", ""); verr == nil {
		t.Error("a grant for one key authorized another")
	}
}

// A grant for a plugin covers its capabilities; the reverse is not true.
func TestNamespaceGrant(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv"})
	if verr := gate(t, declare("kv.get", plugin.Write, "key", true), map[string]any{"key": "any"}, "", ""); verr != nil {
		t.Errorf("a plugin-wide grant did not cover its capability: %v", verr)
	}
	if verr := gate(t, declare("todo.rm", plugin.Destructive, "id", false), map[string]any{"id": "1"}, "", ""); verr == nil {
		t.Error("a kv grant authorized a todo call")
	}
}

// Covering is the same question the gate asks, turned around: not "does
// anything authorize this call" but "after some grants are gone, does
// anything left still authorize it". grant.revoke depends on this to avoid
// reporting "No active grant" while a wider one still stands.
func TestCovering(t *testing.T) {
	grants := []Grant{{Target: "kv"}, {Target: "todo.rm", Scope: "1"}}

	if g := Covering(grants, "kv.get", "", ""); g == nil || g.Target != "kv" {
		t.Errorf("Covering(kv.get) = %v, want the namespace grant", g)
	}
	if g := Covering(grants, "todo.rm", "1", ""); g == nil {
		t.Error("Covering did not find the exact scoped grant")
	}
	if g := Covering(grants, "todo.rm", "2", ""); g != nil {
		t.Errorf("a grant scoped to record 1 wrongly covered record 2: %v", g)
	}
	if g := Covering(nil, "kv.get", "", ""); g != nil {
		t.Errorf("Covering(nil) = %v, want nil", g)
	}
}

// Every record a call names needs cover: two thirds of a leak is a leak.
func TestEveryNamedRecordMustBeCovered(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", Scope: "a"})
	c := declare("kv.env", plugin.Write, "key", true)
	c.Inputs = []plugin.Field{{Name: "key", Type: plugin.StringSlice}}

	if verr := gate(t, c, map[string]any{"key": []string{"a"}}, "", ""); verr != nil {
		t.Errorf("the granted key was refused: %v", verr)
	}
	verr := gate(t, c, map[string]any{"key": []string{"a", "b"}}, "", "")
	if verr == nil {
		t.Fatal("a partly-granted call went through")
	}
	if !strings.Contains(verr.Message, "b") {
		t.Errorf("the refusal should name what is missing: %q", verr.Message)
	}
	// MCP arguments arrive as []any from JSON, not []string.
	if verr := gate(t, c, map[string]any{"key": []any{"a", "b"}}, "", ""); verr == nil {
		t.Error("JSON-shaped arguments bypassed the check")
	}
}

// Naming no record is the widest form of a call, so a scoped grant must not
// cover it: "read this key" cannot become "read every key".
func TestScopedGrantDoesNotCoverAScopelessCall(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", Scope: "a"})
	c := declare("kv.env", plugin.Write, "key", true)
	if verr := gate(t, c, map[string]any{}, "", ""); verr == nil {
		t.Error("a grant for one key authorized exporting everything")
	}
	issue(t, Grant{Target: "kv.env"})
	if verr := gate(t, c, map[string]any{}, "", ""); verr != nil {
		t.Errorf("an unscoped grant did not cover the unscoped call: %v", verr)
	}
}

func TestExpiredGrantsDoNotAuthorize(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", Expires: time.Now().Add(-time.Second)})
	if verr := gate(t, declare("kv.get", plugin.Write, "key", true), map[string]any{"key": "k"}, "", ""); verr == nil {
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
	// A seal envelope, still plain readable JSON: §4.7.11's promise is that
	// "what can the agent do right now?" is answerable without unlocking
	// anything, so the file is authenticated and not encrypted.
	var doc struct {
		Seal   string  `json:"seal"`
		Grants []Grant `json:"grants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("grants are not plain JSON: %v", err)
	}
	if doc.Seal == "" {
		t.Error("the file carries no seal, so anything that can write here can author it")
	}
	got := doc.Grants
	if got[0].Target != "kv.get" || got[0].Scope != "db-password" {
		t.Errorf("round trip = %+v", got[0])
	}
	if !strings.Contains(string(data), "db-password") {
		t.Error("the scope is not readable in the file; it must stay answerable in a hurry")
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
		if verr := call(t, c, map[string]any{"key": "k"}, "", ""); verr != nil {
			t.Fatalf("an unlimited grant stopped authorizing calls: %v", verr)
		}
	}
}

func TestMaxUsesGrantStopsAuthorizingOnceSpent(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 2})
	c := declare("kv.get", plugin.Write, "key", true)
	values := map[string]any{"key": "k"}

	for i := range 2 {
		if verr := call(t, c, values, "", ""); verr != nil {
			t.Fatalf("call %d: refused while uses remained: %v", i+1, verr)
		}
	}
	if verr := gate(t, c, values, "", ""); verr == nil {
		t.Fatal("a grant with 0 uses left still authorized a call")
	}
}

// A regression test for a real bug review caught (PROJECT.md D74): a call
// naming the same record twice in a StringSlice-typed scope (kv.env's
// --key, repeatable) used to spend the covering grant once per occurrence
// rather than once per call, so a single authorized call could burn through
// a multi-use budget meant for that many separate calls.
func TestARepeatedScopeInOneCallSpendsOnlyOnce(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", Scope: "db-password", MaxUses: 2})
	c := plugin.Capability{
		ID: "kv.env", Summary: "kv.env", Safety: plugin.Write, Scope: "key", NeedsGrant: true,
		Inputs: []plugin.Field{{Name: "key", Type: plugin.StringSlice}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	values := map[string]any{"key": []string{"db-password", "db-password"}}

	if verr := call(t, c, values, "", ""); verr != nil {
		t.Fatalf("first call: %v", verr)
	}

	grants, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	if grants[0].Uses != 1 {
		t.Fatalf("Uses = %d after one call naming the same key twice, want 1", grants[0].Uses)
	}

	// The budget was for two calls; one call with a duplicated name must not
	// have spent both.
	if verr := gate(t, c, map[string]any{"key": []string{"db-password"}}, "", ""); verr != nil {
		t.Errorf("a second, independent call was refused — the first call over-spent: %v", verr)
	}
}

// A regression test for a real bug review caught (PROJECT.md D83): unlike
// TestARepeatedScopeInOneCallSpendsOnlyOnce above (the same record named
// twice), this is *distinct* records named in one call — kv.env's --key is
// repeatable, so `kv env --key a --key b --key c` against a --max-uses 1
// grant with no Scope (so it covers every key) named three different
// records in one call. checkAgainst approved all three independently (each
// one individually "covered" by the same grant), then spend incremented
// that single grant three times in the one Save the call made — Uses went
// 0->3 against a budget of 1, and the call itself was never refused: a
// grant documented as "reveal this secret exactly once" revealed three.
func TestDistinctRecordsInOneCallCannotExceedAGrantsBudget(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.env", MaxUses: 1}) // no Scope: covers every key
	c := plugin.Capability{
		ID: "kv.env", Summary: "kv.env", Safety: plugin.Write, Scope: "key", NeedsGrant: true,
		Inputs: []plugin.Field{{Name: "key", Type: plugin.StringSlice}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	values := map[string]any{"key": []string{"a", "b", "c"}}

	release, verr := Reserve(c, values, "", "", "")
	if verr == nil {
		release()
		t.Fatal("a call naming three records was authorized against a one-use grant")
	}

	grants, loadErr := loadAll()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(grants) != 1 || grants[0].Uses != 0 {
		t.Fatalf("a refused call still spent uses: %+v", grants)
	}

	// The budget was real, just too small for three at once: a call naming
	// exactly as many records as the grant can afford must still succeed.
	// Fresh store, so the exhausted-for-this-call (but still Active, since
	// Uses < MaxUses) grant above can't be picked up as a second covering
	// grant and muddy the count.
	setup(t)
	issue(t, Grant{Target: "kv.env", MaxUses: 3})
	_, verr = Reserve(c, values, "", "", "")
	if verr != nil {
		t.Fatalf("a call naming exactly as many records as the budget allows was refused: %v", verr)
	}
	// No release(): that closure is only for a call that goes on to fail —
	// calling it here would refund the very use just asserted as spent.
	grants, loadErr = loadAll()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(grants) != 1 || grants[0].Uses != 3 {
		t.Fatalf("Uses = %+v after three distinct records against a three-use budget, want one grant with Uses=3", grants)
	}
}

// A regression test for a real bug review demonstrated directly
// (PROJECT.md D74): fmt.Sprint(float64(1000000)) is "1e+06", not
// "1000000" — an ordinary six-digit Int-typed Scope value (todo.rm's id,
// for instance) delivered over MCP as a JSON number would never match a
// grant an operator issued for the same id typed as a plain string.
func TestIntScopedGrantMatchesTheOperatorTypedNumber(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "todo.rm", Scope: "1000000"})
	c := plugin.Capability{
		ID: "todo.rm", Summary: "todo.rm", Safety: plugin.Destructive, Scope: "id",
		Inputs: []plugin.Field{{Name: "id", Type: plugin.Int, Required: true}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	// An MCP call's JSON number decodes to float64 before Reserve/Check ever
	// see it — this is that shape, not the CLI's already-typed int.
	if verr := gate(t, c, map[string]any{"id": float64(1000000)}, "", ""); verr != nil {
		t.Errorf("a grant for id 1000000 did not cover the same id as a JSON number: %v", verr)
	}
}

// A coverage test for a real gap review found (PROJECT.md D74): Reserve is,
// by its own doc comment, the whole gate a use-limited grant passes
// through, and its Load()/acquireLock()/Save() failure branches — the
// difference between failing closed and silently authorizing an
// unauthorized call — had no test forcing any of them to actually fail.
// This drives the one that is cheapest to reach honestly: a corrupt grant
// file discovered mid-Reserve, on its fast-path Load(). Reserve must refuse
// the call rather than treat an unreadable grant store as "nothing to
// check".
func TestReserveFailsClosedWhenTheGrantFileIsUnreadable(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: 1})
	c := declare("kv.get", plugin.Write, "key", true)

	if err := os.WriteFile(Path(), []byte(`{"seal":`), 0o600); err != nil {
		t.Fatal(err)
	}

	release, verr := Reserve(c, map[string]any{"key": "k"}, "", "", "")
	if verr == nil {
		if release != nil {
			release()
		}
		t.Fatal("Reserve authorized a call while the grant file could not be read")
	}
}

// A call the grant does not cover is refused, and costs nothing. The second
// half is the one worth pinning: a budget that could be drained by calls it
// never authorized would let an agent exhaust a one-time grant on records it
// was never given, and the operator would find it spent with nothing revealed.
func TestAnUncoveredCallSpendsNothing(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "a", MaxUses: 1})
	c := declare("kv.get", plugin.Write, "key", true)

	if verr := call(t, c, map[string]any{"key": "b"}, "", ""); verr == nil {
		t.Fatal("a call naming an ungranted record was authorized")
	}
	grants, _ := Load()
	if len(grants) != 1 || grants[0].Uses != 0 {
		t.Errorf("a refused call spent a use: %+v", grants)
	}
}

// The whole point of the lock: two goroutines racing to spend the same
// one-time grant must not both see it unspent. The go-sdk dispatches every
// tools/call in its own goroutine, so this is the ordinary case for an agent
// that pipelines two requests, not an exotic one.
//
// **This used to race Consume, which nothing called.** Reserve is the only
// thing that spends a use in the product, and it could be stripped of
// acquireLock entirely with this suite still green — the concurrency test was
// guarding the twin. Racing Reserve, the same edit fails here.
func TestConcurrentReserveDoesNotOverspendAOneTimeGrant(t *testing.T) {
	setup(t)
	const uses = 8
	issue(t, Grant{Target: "kv.get", Scope: "k", MaxUses: uses})
	c := declare("kv.get", plugin.Write, "key", true)
	values := map[string]any{"key": "k"}

	// Twice the budget, all at once. Unlike Consume — which returned nil
	// whether or not it spent anything — Reserve refuses once the budget is
	// gone, so the count of successes is the assertion.
	done := make(chan *view.Error, uses*2)
	for range uses * 2 {
		go func() { done <- call(t, c, values, "", "") }()
	}
	granted := 0
	for range uses * 2 {
		if verr := <-done; verr == nil {
			granted++
		}
	}
	if granted != uses {
		t.Errorf("%d of %d concurrent calls were authorized against a %d-use grant", granted, uses*2, uses)
	}
	grants, verr := Load()
	if verr != nil {
		t.Fatal(verr)
	}
	// Spent exactly to its limit, not past it — Load already drops it once
	// exhausted, so its absence here is itself part of the assertion: a
	// surviving row would mean Uses < MaxUses despite 2x uses worth of calls.
	if len(grants) != 0 {
		t.Errorf("grant survived %d calls against a %d-use limit: %+v", uses*2, uses, grants)
	}
}

// Reserve's fast path decides "nothing to spend" from one unlocked Load, then
// (before the fix this guards) asked Check for an authorization decision —
// which took its own, independent, unlocked Load. A MaxUses grant created in
// the gap between those two reads was invisible to the first (so the fast
// path never touched the lock or spent anything) and visible to the second
// (so the call was authorized anyway): a one-time grant delivered for free,
// reproduced and left open in PROJECT.md D74.
//
// The real gap is a handful of microseconds, too narrow to land reliably by
// launching two goroutines and hoping. This widens it without touching what
// is being raced: Load's own cost (read, unseal, parse, filter, sort) scales
// with how many grants are in the file, so padding the file with thousands of
// unrelated ones stretches the gap between Reserve's own Load and a
// hypothetical second one from microseconds to low milliseconds — comfortably
// past ordinary goroutine start jitter — while every grant that actually
// matters to the assertion is unaffected by how many others sit beside it.
func TestReserveFastPathIsNotFooledByAGrantThatArrivesMidCheck(t *testing.T) {
	setup(t)

	const noise = 4000
	future := time.Now().Add(time.Hour)
	padding := make([]Grant, noise)
	for i := range padding {
		padding[i] = Grant{Target: fmt.Sprintf("noise.cap%d", i), Issued: time.Now(), Expires: future}
	}

	c := declare("kv.get", plugin.Write, "key", true)
	values := map[string]any{"key": "k"}

	const trials = 150
	for trial := range trials {
		if verr := Save(padding); verr != nil {
			t.Fatalf("trial %d: seed: %v", trial, verr)
		}

		done := make(chan struct {
			release func()
			verr    *view.Error
		}, 1)
		go func() {
			release, verr := Reserve(c, values, "", "", "")
			done <- struct {
				release func()
				verr    *view.Error
			}{release, verr}
		}()

		// No synchronization with the goroutine above on purpose — the point
		// is to land this write near Reserve's own Load without knowing
		// exactly when that happens, the same way a real concurrent grant
		// and a real concurrent call would race with no coordination between
		// them either.
		contested := append(append([]Grant{}, padding...), Grant{
			Target: "kv.get", Scope: "k", MaxUses: 1, Issued: time.Now(), Expires: future,
		})
		if verr := Save(contested); verr != nil {
			t.Fatalf("trial %d: inject: %v", trial, verr)
		}

		result := <-done
		if result.release != nil {
			result.release()
		}

		if result.verr == nil {
			// Authorized — the only thing in the file that could ever cover
			// this call is the one-use grant just injected, so it must show
			// the spend. Uses still 0 means the call ran for free.
			after, lverr := loadAll()
			if lverr != nil {
				t.Fatalf("trial %d: reload: %v", trial, lverr)
			}
			for _, g := range after {
				if g.Target == "kv.get" && g.Scope == "k" && g.Uses != 1 {
					t.Fatalf("trial %d: authorized against a MaxUses=1 grant without spending it (Uses=%d)",
						trial, g.Uses)
				}
			}
		}
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

// Releasing removes our lock, not whichever lock happens to be at that path.
//
// A holder whose lock was broken as stale has already been replaced. Removing
// by name on the way out deletes the successor's lock and leaves it inside a
// critical section it believes it has to itself — which is the lost
// revocation this lock was written to prevent, reached by a different route.
func TestReleasingDoesNotRemoveSomebodyElsesLock(t *testing.T) {
	setup(t)
	release, verr := acquireLock()
	if verr != nil {
		t.Fatal(verr)
	}
	path := filepath.Join(paths.Data(), lockFile)

	// What a stale break followed by a fresh acquire leaves behind: the same
	// path, somebody else's token.
	successor := []byte("99999 deadbeefdeadbeefdeadbeefdeadbeef\n")
	if err := os.WriteFile(path, successor, 0o600); err != nil {
		t.Fatal(err)
	}

	release()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the successor's lock was deleted by a release that no longer owned it: %v", err)
	}
	if !bytes.Equal(got, successor) {
		t.Errorf("lock file holds %q, want the successor's token", got)
	}
}

// breakStale itself — the deterministic "did it break the right lock"
// tests — moved to internal/filelock/filelock_test.go along with the
// mechanism; this package only exercises it indirectly, through
// acquireLock and TestStaleLockIsReclaimed above.
