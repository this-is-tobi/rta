package grant

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	core "github.com/this-is-tobi/rule-them-all/internal/grant"
	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
)

func setup(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

// req builds a request from a human surface unless told otherwise.
func req(values map[string]any) plugin.Request {
	return plugin.NewRequest(values, false, true).WithSurface(plugin.SurfaceCLI)
}

func run(t *testing.T, h plugin.Handler, values map[string]any) view.View {
	t.Helper()
	v, err := h(context.Background(), req(values))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return v
}

// catalog stands in for the registry: a gated capability that names a record
// and completes it, so the grant commands can be checked against something
// shaped like the real thing.
func catalog() []plugin.Capability {
	return []plugin.Capability{
		{
			ID: "kv.get", Summary: "Reveal a stored value", Safety: plugin.Write,
			NeedsGrant: true, Scope: "key",
			Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Suggest: func(context.Context, plugin.Request) []string {
				return []string{"db-password\tthe database", "api-token"}
			}}},
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		},
		{
			ID: "todo.list", Summary: "List tasks", Safety: plugin.Read,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		},
		{
			ID: "kv.env", Summary: "Export stored values", Safety: plugin.Write, NeedsGrant: true,
			Run: func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		},
		{
			ID: "todo.rm", Summary: "Remove a task", Safety: plugin.Destructive, Scope: "id",
			Inputs: []plugin.Field{{Name: "id", Type: plugin.Int}},
			Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
		},
	}
}

// listH adapts runList the same way allowH does below: closed over the test
// catalog, since the detail page derives what an agent can reach from it.
func listH(ctx context.Context, req plugin.Request) (view.View, error) {
	return runList(ctx, req, catalog)
}

// allowH adapts runAllow (now closed over catalog, like every other target
// lookup in this package) to the plugin.Handler shape the rest of this file
// drives its capabilities through.
func allowH(ctx context.Context, req plugin.Request) (view.View, error) {
	return runAllow(ctx, req, catalog)
}

func TestPluginValidates(t *testing.T) {
	if err := Plugin(catalog).Validate(); err != nil {
		t.Fatal(err)
	}
}

// What may be granted is derived from the registry, so a plugin added
// tomorrow is grantable without anyone editing a list here.
func TestTargetCompletionComesFromTheCatalogue(t *testing.T) {
	setup(t)
	f := inputOf(t, "grant.allow", "target")
	got := f.Candidates(context.Background(), req(nil))

	var joined string
	for _, c := range got {
		joined += c + "\n"
	}
	for _, want := range []string{"kv.get\t", "kv\t"} {
		if !strings.Contains(joined, want) {
			t.Errorf("target completion missing %q: %v", want, got)
		}
	}
	// A read needs no grant, so offering it would be offering a no-op.
	if strings.Contains(joined, "todo.list") {
		t.Errorf("an ungated capability was offered as a grant target: %v", got)
	}
}

// The record a grant narrows to is completed by the target itself — nothing
// in this package knows what a kv key is.
func TestScopeCompletionDelegatesToTheTarget(t *testing.T) {
	setup(t)
	f := inputOf(t, "grant.allow", "scope")
	got := f.Candidates(context.Background(), req(map[string]any{"target": "kv.get"}))
	if len(got) != 2 || plugin.CandidateValue(got[0]) != "db-password" {
		t.Errorf("scope completion = %v, want the target's own keys", got)
	}
	// A target that names no record completes to nothing rather than to
	// everything.
	if got := f.Candidates(context.Background(), req(map[string]any{"target": "todo.list"})); len(got) != 0 {
		t.Errorf("scope completion for an unscoped target = %v", got)
	}
}

// Revoking completes from the grants that exist, not from what could exist.
func TestRevokeCompletesFromHeldGrants(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password"})

	targets := inputOf(t, "grant.revoke", "target").Candidates(context.Background(), req(nil))
	if len(targets) != 1 || targets[0] != "kv.get" {
		t.Errorf("revoke targets = %v, want only what is granted", targets)
	}
	scopes := inputOf(t, "grant.revoke", "scope").
		Candidates(context.Background(), req(map[string]any{"target": "kv.get"}))
	if len(scopes) != 1 || scopes[0] != "db-password" {
		t.Errorf("revoke scopes = %v", scopes)
	}
}

func inputOf(t *testing.T, capID, field string) plugin.Field {
	t.Helper()
	for _, c := range Plugin(catalog).Capabilities {
		if c.ID != capID {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == field {
				return f
			}
		}
	}
	t.Fatalf("no input %s on %s", field, capID)
	return plugin.Field{}
}

func TestAllowThenList(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "ttl": "1h", "note": "deploy"})

	v := run(t, listH, nil)
	tbl, ok := v.(view.Table)
	if !ok {
		t.Fatalf("list = %s", view.TypeOf(v))
	}
	if len(tbl.Rows) != 1 || tbl.Rows[0][0] != "kv.get" || tbl.Rows[0][1] != "db-password" {
		t.Fatalf("rows = %v", tbl.Rows)
	}
	if tbl.Rows[0][3] != "unlimited" {
		t.Errorf("a grant with no --max-uses should read unlimited: %v", tbl.Rows[0])
	}
	if tbl.Rows[0][4] != "deploy" {
		t.Errorf("the note is what makes a stale grant explainable: %v", tbl.Rows[0])
	}
}

// An unscoped grant covers every record, and says so rather than showing a
// blank column that reads like missing data.
func TestUnscopedGrantSaysAny(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "todo"})
	tbl := run(t, listH, nil).(view.Table)
	if tbl.Rows[0][1] != "any" {
		t.Errorf("record = %q, want \"any\"", tbl.Rows[0][1])
	}
}

// An agent that could grant itself access would make the whole mechanism
// theatre.
func TestAgentsCannotIssueGrants(t *testing.T) {
	setup(t)
	_, err := allowH(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.get"}, false, true).WithSurface(plugin.SurfaceMCP))
	var verr *view.Error
	if !asError(err, &verr) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if verr.Code != "grant.human" {
		t.Errorf("code = %s", verr.Code)
	}
	grants, _ := core.Load()
	if len(grants) != 0 {
		t.Errorf("the refused grant was written anyway: %+v", grants)
	}
}

// The cap is the point: a grant whose whole purpose is expiring should not be
// talked into lasting a week.
func TestTTLIsCapped(t *testing.T) {
	setup(t)
	v := run(t, allowH, map[string]any{"target": "kv.get", "ttl": "72h"})
	body := v.(view.Text).Body
	if !strings.Contains(body, "capped") {
		t.Errorf("a capped grant should say so: %q", body)
	}
	grants, _ := core.Load()
	if len(grants) != 1 || grants[0].Expires.After(time.Now().Add(core.MaxTTL+time.Minute)) {
		t.Errorf("grant outlived the cap: %+v", grants)
	}
}

func TestBadTTLIsRefused(t *testing.T) {
	setup(t)
	for _, ttl := range []string{"soon", "-5m", "0"} {
		if _, err := allowH(context.Background(), req(map[string]any{"target": "kv.get", "ttl": ttl})); err == nil {
			t.Errorf("ttl %q was accepted", ttl)
		}
	}
}

// Re-allowing the same thing extends it rather than stacking two grants whose
// earlier expiry would mean nothing.
func TestReallowingExtends(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "k", "ttl": "1m"})
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "k", "ttl": "2h"})
	grants, _ := core.Load()
	if len(grants) != 1 {
		t.Fatalf("grants = %+v, want one", grants)
	}
	if time.Until(grants[0].Expires) < time.Hour {
		t.Errorf("the later grant did not win: %+v", grants[0])
	}
}

// Revoking a plugin has to take back everything inside it: the point of
// typing it in a hurry is that nothing kv-shaped survives.
func TestRevokingAPluginTakesBackItsCapabilities(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "a"})
	run(t, allowH, map[string]any{"target": "kv.env"})
	run(t, allowH, map[string]any{"target": "todo.rm"})

	run(t, runRevoke, map[string]any{"target": "kv"})
	grants, _ := core.Load()
	if len(grants) != 1 || grants[0].Target != "todo.rm" {
		t.Errorf("after revoking kv: %+v", grants)
	}
}

func TestRevokeAll(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get"})
	run(t, allowH, map[string]any{"target": "todo.rm"})
	run(t, runRevoke, map[string]any{"all": true})
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("--all left %+v", grants)
	}
}

// A row naming exactly the target can be gone while a wider grant still
// authorizes every call that target would ever make. Reporting only whether
// a matching row existed — without saying whether the target is actually
// still reachable — is a lie by omission: "No active grant" read as "an
// agent cannot do this", and an agent still could, via the untouched
// namespace grant.
func TestRevokeSaysWhenAWiderGrantStillCoversTheTarget(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv"})

	body := run(t, runRevoke, map[string]any{"target": "kv.get"}).(view.Text).Body
	if !strings.Contains(body, "still covered") || !strings.Contains(body, "kv") {
		t.Errorf("no warning about the surviving namespace grant: %q", body)
	}
	// And it must be telling the truth: the namespace grant is untouched.
	grants, _ := core.Load()
	if len(grants) != 1 || grants[0].Target != "kv" {
		t.Errorf("the namespace grant should have survived a revoke of kv.get: %+v", grants)
	}

	// Take the namespace grant too, and the warning must go away — kv.get is
	// genuinely uncovered now.
	body = run(t, runRevoke, map[string]any{"target": "kv"}).(view.Text).Body
	if strings.Contains(body, "still covered") {
		t.Errorf("warned about coverage after removing the only covering grant: %q", body)
	}
	body = run(t, runRevoke, map[string]any{"target": "kv.get"}).(view.Text).Body
	if strings.Contains(body, "still covered") {
		t.Errorf("false warning with nothing left to cover it: %q", body)
	}
}

// A --dry-run must warn too: the whole point of previewing is knowing what
// revoking will (and will not) accomplish before committing to it.
func TestRevokeDryRunAlsoWarnsAboutCoverage(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv"})

	dry, err := runRevoke(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.get"}, true, true).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(dry.(view.Text).Body, "still covered") {
		t.Errorf("dry-run did not warn about the surviving namespace grant: %q", dry.(view.Text).Body)
	}
	// And it must actually have changed nothing.
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("dry-run revoke changed state: %+v", grants)
	}
}

func TestRevokeNeedsATarget(t *testing.T) {
	setup(t)
	if _, err := runRevoke(context.Background(), req(nil)); err == nil {
		t.Error("revoke with no target and no --all was accepted")
	}
}

// A dry run must not change what an agent may do.
func TestDryRunGrantsNothing(t *testing.T) {
	setup(t)
	_, err := allowH(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.get"}, true, true).WithSurface(plugin.SurfaceCLI))
	if err != nil {
		t.Fatal(err)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("dry run wrote %+v", grants)
	}
}

// A grant that authorizes nothing is worse than an error: `grant list`
// would show it looking exactly like a working one, so a typo reads back as
// "done" right up until the agent's call is refused anyway.
func TestUnknownTargetIsRefused(t *testing.T) {
	setup(t)
	_, err := allowH(context.Background(), req(map[string]any{"target": "kv.gett"}))
	var verr *view.Error
	if !asError(err, &verr) || verr.Code != "grant.unknowntarget" {
		t.Fatalf("err = %v, want a refusal naming the unknown target", err)
	}
	if grants, _ := core.Load(); len(grants) != 0 {
		t.Errorf("the refused grant was written anyway: %+v", grants)
	}
}

// A namespace target is not itself a capability ID, and must not be treated
// as an unknown one.
func TestNamespaceTargetIsStillAccepted(t *testing.T) {
	setup(t)
	if _, err := allowH(context.Background(), req(map[string]any{"target": "kv"})); err != nil {
		t.Fatalf("a real namespace was refused: %v", err)
	}
}

// Consent state belongs to the person at the terminal in both directions —
// an agent that can erase a grant is exactly as much of a hole as one that
// can issue itself one.
func TestMaxUsesIsRecordedAndShownInList(t *testing.T) {
	setup(t)
	v := run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "max-uses": 1})
	if !strings.Contains(v.(view.Text).Body, "once") {
		t.Errorf("a --max-uses 1 grant should say so plainly: %q", v.(view.Text).Body)
	}
	grants, _ := core.Load()
	if len(grants) != 1 || grants[0].MaxUses != 1 {
		t.Fatalf("grants = %+v, want MaxUses 1", grants)
	}
	tbl := run(t, listH, nil).(view.Table)
	if tbl.Rows[0][3] != "1" {
		t.Errorf("uses left = %q, want 1", tbl.Rows[0][3])
	}
}

func TestNegativeMaxUsesIsRefused(t *testing.T) {
	setup(t)
	if _, err := allowH(context.Background(), req(map[string]any{"target": "kv.get", "max-uses": -1})); err == nil {
		t.Fatal("a negative --max-uses was accepted")
	}
}

func TestMaxUsesGrantIsSpentAfterASuccessfulCall(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "max-uses": 1})
	c := catalog()[0] // kv.get
	values := map[string]any{"key": "db-password"}

	if verr := core.Check(c, values); verr != nil {
		t.Fatalf("the fresh grant was refused: %v", verr)
	}
	if verr := core.Consume(c, values); verr != nil {
		t.Fatalf("consume: %v", verr)
	}
	if verr := core.Check(c, values); verr == nil {
		t.Fatal("a one-time grant still authorized a second call")
	}
}

func TestAgentsCannotRevokeGrants(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "a"})

	_, err := runRevoke(context.Background(),
		plugin.NewRequest(map[string]any{"target": "kv.get"}, false, true).WithSurface(plugin.SurfaceMCP))
	var verr *view.Error
	if !asError(err, &verr) || verr.Code != "grant.human" {
		t.Fatalf("err = %v, want a refusal", err)
	}
	if grants, _ := core.Load(); len(grants) != 1 {
		t.Errorf("the refused revoke changed grant state anyway: %+v", grants)
	}
}

func asError(err error, target **view.Error) bool {
	ve, ok := err.(*view.Error)
	if ok {
		*target = ve
	}
	return ok
}

// "What did I allow" is only half of "what can an agent do here". The detail
// page answers both, and the two halves must together account for every
// capability in the catalogue — a capability in neither is one nobody can
// see the permissions of.
func TestDetailedListSplitsEveryCapabilityByWhetherItNeedsAGrant(t *testing.T) {
	setup(t)
	v := run(t, listH, map[string]any{"detail": true})
	s, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detailed list = %s, want Sections", view.TypeOf(v))
	}
	seen := map[string]string{}
	for _, item := range s.Items {
		tbl, ok := item.View.(view.Table)
		if !ok || item.Title == "granted" {
			continue
		}
		for _, row := range tbl.Rows {
			if prev, dup := seen[row[0]]; dup {
				t.Errorf("%s appears in both %q and %q", row[0], prev, item.Title)
			}
			seen[row[0]] = item.Title
		}
	}
	for _, c := range catalog() {
		where, ok := seen[c.ID]
		if !ok {
			t.Errorf("%s is in neither half of the page", c.ID)
			continue
		}
		want := "reachable by default"
		switch {
		case core.Required(c):
			want = "needs a grant a person issues"
		case c.Safety != plugin.Read:
			want = "needs --allow-write on the server"
		}
		if where != want {
			t.Errorf("%s listed under %q, want %q", c.ID, where, want)
		}
	}
}

// The compact view is what the dashboard tile refreshes; it must stay the
// plain table and never expand into the page.
func TestCompactListIsNotAPage(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "ttl": "15m"})
	if v := run(t, listH, nil); view.TypeOf(v) == view.TypeOf(view.Sections{}) {
		t.Fatalf("compact list = %s, want the flat table", view.TypeOf(v))
	}
}

// A revocation has to survive a call that is being authorized at the same
// instant. runRevoke read the grant file, filtered it and wrote it back with
// no lock, while core.Reserve held one across its own read-modify-write: a
// revoke that landed inside Reserve's window was overwritten by the snapshot
// Reserve had taken before it, so the grant the operator had just been told
// was revoked was in the file again a millisecond later, still authorizing
// calls and still listed by `grant list`.
func TestRevokeIsNotUndoneByAConcurrentReserve(t *testing.T) {
	setup(t)
	c := catalog()[0] // kv.get: needs a grant, and names a record
	values := map[string]any{"key": "db-password"}

	for round := range 30 {
		// --max-uses is what makes Reserve write at all: with no use-limited
		// grant in the file it never takes the lock and never saves, which is
		// exactly why this was a coincidence rather than a certainty.
		run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password", "max-uses": 32})

		var wg sync.WaitGroup
		for range 6 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// The release is dropped on purpose: these stand for calls
				// that succeeded, and a refund would only put uses back into
				// a file this test reads for presence, not for arithmetic.
				// Being refused is the correct outcome once the revoke lands.
				_, _ = core.Reserve(c, values)
			}()
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := runRevoke(context.Background(), req(map[string]any{"target": "kv.get"})); err != nil {
				t.Errorf("revoke: %v", err)
			}
		}()
		wg.Wait()

		grants, verr := core.Load()
		if verr != nil {
			t.Fatalf("round %d: %v", round, verr)
		}
		if len(grants) != 0 {
			t.Fatalf("round %d: a revoked grant was still authorizing calls afterwards: %+v", round, grants)
		}
	}
}

// Revoking one target must not quietly delete another target's spent grant.
// A row spent to its last use is invisible to Load, so a revoke that
// round-tripped through Load erased it just by saving — and refund, the only
// thing that can give that use back after the guarded call fails, then found
// nothing to give it back to.
func TestRevokingOneTargetKeepsTheSpentGrantAnotherCallCanStillRefund(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password"})
	// grant.allow only ever issues fresh grants, so the spent row goes in
	// through core.Save, the way a call that used its last use left it.
	held, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	now := time.Now()
	spent := core.Grant{Target: "todo.rm", Scope: "7", Issued: now, Expires: now.Add(time.Hour), MaxUses: 1, Uses: 1}
	if verr := core.Save(append(held, spent)); verr != nil {
		t.Fatal(verr)
	}

	run(t, runRevoke, map[string]any{"target": "kv.get"})

	data, err := os.ReadFile(core.Path())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Grants []core.Grant `json:"grants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	stored := doc.Grants
	if len(stored) != 1 || stored[0].Target != "todo.rm" || stored[0].Uses != 1 {
		t.Fatalf("revoking kv.get left the file as %+v", stored)
	}
}

// The file now keeps rows Load hides, so the coverage warning has to filter
// them itself. An expired namespace grant authorizes nothing, and telling an
// operator that kv.get is "still covered by an active grant on kv" would send
// them off to revoke something that had already lapsed.
func TestALapsedGrantIsNotReportedAsStillCovering(t *testing.T) {
	setup(t)
	run(t, allowH, map[string]any{"target": "kv.get", "scope": "db-password"})
	held, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	now := time.Now()
	lapsed := core.Grant{Target: "kv", Issued: now.Add(-time.Hour), Expires: now.Add(-time.Minute)}
	if verr := core.Save(append(held, lapsed)); verr != nil {
		t.Fatal(verr)
	}

	body := run(t, runRevoke, map[string]any{"target": "kv.get", "scope": "db-password"}).(view.Text).Body
	if strings.Contains(body, "still covered") {
		t.Errorf("an expired namespace grant was reported as covering kv.get: %q", body)
	}
}
