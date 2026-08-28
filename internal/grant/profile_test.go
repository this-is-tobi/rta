package grant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// A capability shaped like plugins/pg's: Safety Read, no NeedsGrant, and a
// connection an operator configures. Every one of pg's six is this shape, and
// they are the reason Required grew a profile clause.
func readCap() plugin.Capability {
	return plugin.Capability{
		ID: "pg.query", Summary: "run a query", Safety: plugin.Read,
		Inputs: []plugin.Field{
			{Name: "host", Type: plugin.String, Config: "host", Local: true},
			{Name: "sql", Type: plugin.String},
		},
	}
}

// Naming a profile is what makes consent required, and that clause is the
// whole feature.
//
// Without it a profile-aware covers() does nothing at all for the plugin the
// feature exists for: plugins/pg declares no NeedsGrant and all six of its
// capabilities are Safety: Read — honestly, since pg.query runs inside a READ
// ONLY transaction — so Required short-circuits false and no pg call ever
// reaches a grant. "Read the database the operator configured" would silently
// become "read any database the operator has configured".
func TestNamingAProfileIsItselfWhatNeedsAGrant(t *testing.T) {
	c := readCap()
	if Required(c, "") {
		t.Error("a plain read against the base connection asks for consent nobody needs to give")
	}
	if !Required(c, "prod") {
		t.Error("a read against a named connection was let through with no grant at all")
	}
}

// A grant is exact about which connection it is for, in both directions.
//
// Scope's empty-means-every-record does not transfer, and this is the test
// that says so. Scope wildcards a *record* inside a connection the operator
// already chose; a profile wildcard would be a *connection* wildcard — one
// grant issued while pointed at a scratch database authorizing the identical
// call against production.
func TestAGrantForOneConnectionAuthorizesNoOther(t *testing.T) {
	staging := Grant{Target: "pg", Profile: "staging"}
	base := Grant{Target: "pg"}

	if staging.covers("pg.query", "", "prod") {
		t.Error("a grant for staging authorized a call against prod")
	}
	if staging.covers("pg.query", "", "") {
		t.Error("a grant for staging authorized a call against the base connection")
	}
	if !staging.covers("pg.query", "", "staging") {
		t.Error("a grant for staging did not authorize a call against staging")
	}
	// And the direction that matters most for everything already on disk.
	if base.covers("pg.query", "", "prod") {
		t.Error("a grant naming no profile authorized a call against prod — " +
			"every grant issued before profiles existed just widened to every connection")
	}
	if !base.covers("pg.query", "", "") {
		t.Error("a grant naming no profile stopped authorizing the calls it was issued for")
	}
}

// The seal survives the new fields.
//
// canonical() is json.Marshal over the parsed []Grant, so a field that is the
// zero string on every stored grant must be omitted and re-encode
// byte-identically. Without omitempty, old rows re-encode with "profile":""
// and "ttl":"", hmac.Equal fails, and rta reports its own file as forged —
// taking `grant list`, `rta doctor`, the dashboard tile and every gated MCP
// call down at once.
func TestAGrantFileSealedBeforeProfilesExistedStillVerifies(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RTA_DATA_DIR", dir)

	// Written the way the old code would have: no profile, no ttl.
	now := time.Now()
	original := []Grant{{
		Target:  "kv.get",
		Scope:   "db-password",
		Issued:  now,
		Expires: now.Add(time.Hour),
		MaxUses: 1,
	}}
	if verr := Save(original); verr != nil {
		t.Fatal(verr)
	}

	// The bytes on disk must not mention the new fields at all — that is what
	// makes the re-encode byte-identical for a file written by an older build.
	data, err := os.ReadFile(filepath.Join(dir, "grants.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Grants []map[string]any `json:"grants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Grants) != 1 {
		t.Fatalf("expected one stored grant, got %d", len(doc.Grants))
	}
	for _, absent := range []string{"profile", "ttl", "profilePin"} {
		if _, present := doc.Grants[0][absent]; present {
			t.Errorf("an empty %q was written to the file; every seal made by an older "+
				"build now fails to verify", absent)
		}
	}

	// And it loads back, seal intact.
	loaded, verr := Load()
	if verr != nil {
		t.Fatalf("a grant file with no profile field no longer verifies: %v", verr)
	}
	if len(loaded) != 1 || loaded[0].Target != "kv.get" || loaded[0].Scope != "db-password" {
		t.Errorf("loaded = %+v, want the stored grant unchanged", loaded)
	}
	if loaded[0].Profile != "" {
		t.Errorf("profile = %q, want empty — an old grant must not acquire a connection", loaded[0].Profile)
	}
}

// A use spent against one connection is not spent against another.
//
// allocate() is shared between the authorization decision and the spend, so a
// profile the gate did not consider would be a grant spent for a call it never
// authorized.
func TestUsesAreSpentPerConnection(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := readCap()
	now := time.Now()
	// Each grant carries the fingerprint of its own connection, which is what
	// one issued by `rta grant allow` carries.
	const stagingPin, prodPin = "staging-conn", "prod-conn"
	if verr := Save([]Grant{
		{Target: "pg", Profile: "staging", ProfilePin: stagingPin,
			Issued: now, Expires: now.Add(time.Hour), MaxUses: 1},
		{Target: "pg", Profile: "prod", ProfilePin: prodPin,
			Issued: now, Expires: now.Add(time.Hour), MaxUses: 1},
	}); verr != nil {
		t.Fatal(verr)
	}

	release, verr := Reserve(c, map[string]any{"sql": "select 1"}, "staging", stagingPin, "")
	if verr != nil {
		t.Fatalf("the staging grant did not authorize a staging call: %v", verr)
	}
	_ = release

	// staging is spent; prod is untouched.
	if _, verr := Reserve(c, map[string]any{"sql": "select 1"}, "staging", stagingPin, ""); verr == nil {
		t.Error("a one-time grant for staging authorized a second staging call")
	}
	if _, verr := Reserve(c, map[string]any{"sql": "select 1"}, "prod", prodPin, ""); verr != nil {
		t.Errorf("spending the staging grant also spent the prod one: %v", verr)
	}
}

// Window is what renew extends by, and it survives a grant sealed before the
// TTL field existed.
func TestWindowFallsBackToTheIssuedLifetime(t *testing.T) {
	now := time.Now()
	old := Grant{Issued: now, Expires: now.Add(90 * time.Minute)}
	if got := old.Window(); got != 90*time.Minute {
		t.Errorf("Window() = %v, want the 90m the grant was issued with", got)
	}
	recorded := Grant{TTL: "15m", Issued: now, Expires: now.Add(90 * time.Minute)}
	if got := recorded.Window(); got != 15*time.Minute {
		t.Errorf("Window() = %v, want the recorded 15m", got)
	}
	// A hand-edited file must not buy a longer renewal than a person could ask
	// for.
	forged := Grant{TTL: "9000h", Issued: now, Expires: now.Add(time.Hour)}
	if got := forged.Window(); got != MaxTTL {
		t.Errorf("Window() = %v, want it clamped to %v", got, MaxTTL)
	}
}

// The operator's switched-on environment bounds a grant, and bounds nothing
// else.
//
// The rule in one place: while `active` names an environment, a grant naming a
// *different* one does not count. A grant naming none still covers the calls it
// always covered, which is most of them — the first version of this lived in
// internal/mcp as a comparison rather than a filter, missed that case, and
// refused every call that named no profile at all.
func TestASwitchedOnEnvironmentBoundsOnlyOtherProfiles(t *testing.T) {
	now := time.Now()
	grants := []Grant{
		{Target: "pg", Profile: "staging", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg", Profile: "prod", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "sys.gated", Issued: now, Expires: now.Add(time.Hour)},
	}
	for _, tc := range []struct {
		active string
		want   []string // profiles surviving, "" for the base grant
	}{
		{active: "", want: []string{"staging", "prod", ""}},
		{active: "staging", want: []string{"staging", ""}},
		{active: "prod", want: []string{"prod", ""}},
		// A profile nothing was granted for leaves only the base grant — the
		// switch subtracts, so it can leave less, never more.
		{active: "somewhere-else", want: []string{""}},
	} {
		var got []string
		view, _ := reachable(grants, "", "", tc.active)
		for _, g := range view {
			got = append(got, g.Profile)
		}
		if len(got) != len(tc.want) {
			t.Errorf("active=%q kept %v, want %v", tc.active, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("active=%q kept %v, want %v", tc.active, got, tc.want)
				break
			}
		}
	}
	// And it never adds: the reachable set is a subset, whatever is switched on.
	for _, active := range []string{"", "staging", "prod", "nothing-like-this"} {
		view, at := reachable(grants, "", "", active)
		if len(view) > len(grants) {
			t.Errorf("active=%q produced more grants than were stored", active)
		}
		// Every survivor reports where it came from, or Reserve cannot write
		// its spend back to the right row.
		if len(at) != len(view) {
			t.Errorf("active=%q returned %d grants and %d positions", active, len(view), len(at))
		}
		for i, pos := range at {
			if grants[pos].Profile != view[i].Profile {
				t.Errorf("active=%q: position %d points at %q, not the grant returned (%q)",
					active, pos, grants[pos].Profile, view[i].Profile)
			}
		}
	}
}

// A grant is bound to the connection, not to the name of one.
//
// Profile narrows a grant to an environment, and an environment is a name over
// a set of values somebody can edit. Editing them repointed every live grant
// naming it: the operator consented to a call reaching staging, and the
// identical grant went on authorizing it against whatever that name meant
// afterwards — a credential redirect with the consent record still reading
// "staging". ADR 0019 and ADR 0020 both record it as required the moment a
// connection can also carry cluster coordinates.
func TestAGrantStopsCoveringAConnectionThatWasRepointed(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := plugin.Capability{ID: "pg.query", Summary: "q", Safety: plugin.Read}
	values := map[string]any{"sql": "select 1"}

	now := time.Now()
	if verr := Save([]Grant{{
		Target: "pg", Profile: "staging", ProfilePin: "as-consented",
		Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}

	if verr := gate(t, c, values, "staging", "as-consented"); verr != nil {
		t.Fatalf("the connection that was consented to was refused: %v", verr)
	}
	verr := gate(t, c, values, "staging", "edited-since")
	if verr == nil {
		t.Fatal("a grant covered a connection it was not issued against")
	}
	// **The same sentence an ungranted call gets, and that is deliberate.**
	// "This profile changed since you were granted" would separate "granted,
	// then edited" from "never granted", telling an agent that the profile
	// exists and that consent was once given for it. The remedy is a person's
	// to find, in `rta grant list` and `rta doctor`.
	if verr.Code != "core.grant.required" {
		t.Errorf("code = %q, want the refusal an ungranted call gets", verr.Code)
	}
	ungranted := gate(t, c, values, "other", "whatever")
	if ungranted == nil || ungranted.Message != verr.Message {
		t.Errorf("a repointed connection is distinguishable from an ungranted one:\n  %v\n  %v",
			verr, ungranted)
	}
}

// A grant that names no profile carries no pin, and nothing about it changes.
//
// That is what makes the field's arrival safe for everything already on disk,
// and it is the same argument Profile's own comment makes one field up: an
// unprofiled grant names no connection that could have been repointed.
func TestAnUnprofiledGrantIsUntouchedByThePin(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := plugin.Capability{ID: "kv.get", Summary: "g", Safety: plugin.Write, NeedsGrant: true, Scope: "key"}
	values := map[string]any{"key": "db-password"}

	now := time.Now()
	if verr := Save([]Grant{{
		Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	// Whatever pin is computed for a call that names no profile, the grant
	// still covers it: the filter does not run at all without a name.
	for _, pin := range []string{"", "anything", "as-consented"} {
		if verr := gate(t, c, values, "", pin); verr != nil {
			t.Errorf("pin %q refused an unprofiled grant: %v", pin, verr)
		}
	}
}

// A profiled grant issued before pins existed is refused, not honoured.
//
// The two other readings are both wrong. "Empty matches anything" rebuilds the
// hole for every grant already on disk and makes the empty pin the default a
// blind-writing attacker produces — Profile's own no-wildcard argument, one
// field along. "Empty matches nothing" is this, and it costs the operator a
// re-consent that self-heals within one TTL, which is at most a day.
func TestAProfiledGrantWithNoPinAuthorizesNothing(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := plugin.Capability{ID: "pg.query", Summary: "q", Safety: plugin.Read}
	values := map[string]any{"sql": "select 1"}

	now := time.Now()
	if verr := Save([]Grant{{
		Target: "pg", Profile: "staging", Issued: now, Expires: now.Add(time.Hour),
	}}); verr != nil {
		t.Fatal(verr)
	}
	for _, pin := range []string{"", "any-stamp"} {
		if verr := gate(t, c, values, "staging", pin); verr == nil {
			t.Errorf("a pinless profiled grant authorized a call at pin %q", pin)
		}
	}
}

// Revocation must keep working on exactly the grants somebody most wants gone.
//
// Covering() is what `rta grant revoke` asks in the other direction — "after I
// remove these, is this still allowed" — and a grant whose connection has been
// repointed is precisely the row an operator reaches for. Had the pin gone
// into covers() rather than into a filter, revoke would have gone deaf on
// those rows and reported "nothing to revoke" about live access.
func TestRevocationStillSeesAGrantWhoseConnectionMoved(t *testing.T) {
	now := time.Now()
	grants := []Grant{{
		Target: "pg", Profile: "staging", ProfilePin: "as-consented",
		Issued: now, Expires: now.Add(time.Hour),
	}}
	if Covering(grants, "pg.query", "", "staging") == nil {
		t.Error("revoke cannot see a grant whose connection has been repointed")
	}
	// And Stale says so, which is what the person-facing surfaces print.
	if !grants[0].Stale("edited-since") {
		t.Error("a repointed grant does not report as stale")
	}
	if grants[0].Stale("as-consented") {
		t.Error("an untouched grant reports as stale")
	}
}

// A granted call must not delete the operator's other grants.
//
// Reserve makes its decision over a subset — bounded drops what the switched-on
// environment puts out of reach, pinned drops what was consented to against a
// different connection — and then writes the result back. Writing the *subset*
// erased every row the filters had dropped, permanently, on the first
// use-limited call.
//
// It is the hazard Mutate's own comment names — "a writer that round-trips
// through Load deletes every row it was not shown, merely by saving" — reached
// through two filters instead of one. And it destroyed exactly the row the
// person-facing surfaces exist to show: a grant whose connection was repointed
// is listed as `(changed)` so the operator can re-consent, and one unrelated
// agent call took that record away.
func TestAGrantedCallDoesNotDeleteTheGrantsItWasNotShown(t *testing.T) {
	readCapFor := func(id string) plugin.Capability {
		return plugin.Capability{ID: id, Summary: "q", Safety: plugin.Read}
	}
	now := time.Now()

	for _, tc := range []struct {
		name    string
		grants  []Grant
		profile string
		pin     string
		active  string
		// survivor is the grant that must still be on disk afterwards.
		survivor func(Grant) bool
		why      string
	}{
		{
			name: "a row the pin filter dropped",
			grants: []Grant{
				// The row `rta grant list` marks (changed) and doctor tells the
				// operator to re-issue.
				{Target: "pg", Profile: "staging", ProfilePin: "as-consented",
					Issued: now, Expires: now.Add(time.Hour)},
				{Target: "pg", Profile: "staging", ProfilePin: "current",
					Issued: now, Expires: now.Add(time.Hour), MaxUses: 3},
			},
			profile:  "staging",
			pin:      "current",
			survivor: func(g Grant) bool { return g.ProfilePin == "as-consented" },
			why:      "the stale-pinned row the operator needs to see",
		},
		{
			// Pins are per namespace, so one profile configuring two plugins
			// gives every call a pin that matches one grant and not the other.
			// This is the ordinary case, not an edge one.
			name: "a grant on another plugin in the same environment",
			grants: []Grant{
				{Target: "kv.get", Scope: "db-password", Profile: "staging", ProfilePin: "kv-conn",
					Issued: now, Expires: now.Add(time.Hour)},
				{Target: "pg", Profile: "staging", ProfilePin: "pg-conn",
					Issued: now, Expires: now.Add(time.Hour), MaxUses: 3},
			},
			profile:  "staging",
			pin:      "pg-conn",
			survivor: func(g Grant) bool { return g.Target == "kv.get" },
			why:      "a grant for a different plugin the call never touched",
		},
		{
			name: "a grant the switched-on environment put out of reach",
			grants: []Grant{
				{Target: "pg", Profile: "prod", ProfilePin: "prod-conn",
					Issued: now, Expires: now.Add(time.Hour)},
				{Target: "pg", Profile: "staging", ProfilePin: "staging-conn",
					Issued: now, Expires: now.Add(time.Hour), MaxUses: 3},
			},
			profile:  "staging",
			pin:      "staging-conn",
			active:   "staging",
			survivor: func(g Grant) bool { return g.Profile == "prod" },
			why:      "a grant for the environment they are not currently in",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RTA_DATA_DIR", t.TempDir())
			if verr := Save(tc.grants); verr != nil {
				t.Fatal(verr)
			}
			release, verr := Reserve(readCapFor("pg.query"),
				map[string]any{"sql": "select 1"}, tc.profile, tc.pin, tc.active)
			if verr != nil {
				t.Fatalf("the covering grant did not authorize the call: %v", verr)
			}
			_ = release

			after, verr := Load()
			if verr != nil {
				t.Fatal(verr)
			}
			found := false
			for _, g := range after {
				if tc.survivor(g) {
					found = true
				}
			}
			if !found {
				t.Errorf("one granted call erased %s; %d grant(s) left: %+v",
					tc.why, len(after), after)
			}
		})
	}
}

// A refund gives back what the call took, from the grants it took it from.
//
// The first version recomputed: it walked the call's scopes and gave a use back
// to the first grant covering each, restarting the search every time. A call
// that spent on two different grants — the ordinary shape when a wide grant and
// a narrow one both cover a multi-record call — then walked into the same grant
// twice. The operator's budget stayed the same size and *moved*: a use they had
// deliberately scoped to one record came back as a use on any record, and the
// narrow grant stayed paid for a call that failed.
//
// Recomputing is wrong for a second reason as well. It asks its question of the
// state after the spend, which other calls may have changed in between, so the
// only correct answer is the one this call already knew.
func TestARefundGivesBackExactlyWhatTheCallTook(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	c := plugin.Capability{
		ID: "kv.env", Summary: "env", Safety: plugin.Write, NeedsGrant: true, Scope: "key",
		Inputs: []plugin.Field{{Name: "key", Type: plugin.String}},
	}
	now := time.Now()
	// The wide grant expires sooner, so Load sorts it first and the naive walk
	// reaches it for both scopes.
	wide := Grant{Target: "kv", Issued: now, Expires: now.Add(time.Hour), MaxUses: 2, Uses: 1}
	narrow := Grant{Target: "kv", Scope: "b", Issued: now, Expires: now.Add(2 * time.Hour),
		MaxUses: 2, Uses: 1}
	if verr := Save([]Grant{wide, narrow}); verr != nil {
		t.Fatal(verr)
	}

	release, verr := Reserve(c, map[string]any{"key": []string{"a", "b"}}, "", "", "")
	if verr != nil {
		t.Fatalf("the call was refused: %v", verr)
	}
	// Both grants are now spent to their limit by this one call.
	spentState, verr := loadAll()
	if verr != nil {
		t.Fatal(verr)
	}
	for _, g := range spentState {
		if g.Uses != 2 {
			t.Fatalf("setup: %+v spent %d uses, want both grants at their limit", g, g.Uses)
		}
	}

	// The handler fails, so every use this call took comes back — and no more.
	release()

	after, verr := loadAll()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(after) != 2 {
		t.Fatalf("refund left %d grants, want both: %+v", len(after), after)
	}
	for _, g := range after {
		which := "the wide grant"
		if g.Scope != "" {
			which = "the grant scoped to " + g.Scope
		}
		if g.Uses != 1 {
			t.Errorf("%s is at %d uses, want the 1 it had before the call — "+
				"a refund moved budget between grants", which, g.Uses)
		}
	}
}
