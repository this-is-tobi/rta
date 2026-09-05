package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/internal/policy"
	"github.com/this-is-tobi/rta/pkg/view"
)

// withPolicy puts a team ceiling above the working directory.
func withPolicy(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, policy.RepoFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
}

// allowing runs grant.allow the way a person does, returning what they read.
func allowing(t *testing.T, values map[string]any) (string, *view.Error) {
	t.Helper()
	v, err := runAllow(context.Background(), req(values), catalog)
	if err != nil {
		verr, ok := err.(*view.Error)
		if !ok {
			t.Fatalf("runAllow: %v", err)
		}
		return "", verr
	}
	return v.(view.Text).Body, nil
}

// **The clamp is user-visible, so it is pinned.** Load already stops a grant
// that outlives the ceiling, which is the enforcement; this is the other
// half of it — a ceiling that applies has to say so, and it has to
// store what it says, or `grant list` shows a four-hour row that dies in
// fifteen minutes.
func TestATeamCeilingClampsTheTTLAndSaysWhichCeilingBit(t *testing.T) {
	setup(t)
	withPolicy(t, "maxTTL: 15m\n")

	body, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "4h"})
	if verr != nil {
		t.Fatal(verr)
	}
	if !strings.Contains(body, "15m") {
		t.Errorf("the grant was not clamped to the team ceiling: %s", body)
	}
	if !strings.Contains(body, "team's policy") {
		t.Errorf("the message does not say which ceiling bit, so somebody goes and "+
			"edits the wrong thing: %s", body)
	}
	if !strings.Contains(body, policy.RepoFile) {
		t.Errorf("the message does not name the file to edit: %s", body)
	}

	// And what was stored is what was said.
	grants, gverr := core.Load()
	if gverr != nil {
		t.Fatal(gverr)
	}
	if len(grants) != 1 {
		t.Fatalf("stored %d grants, want 1", len(grants))
	}
	if window := grants[0].Expires.Sub(grants[0].Issued); window > 16*time.Minute {
		t.Errorf("stored window is %v — the message said 15m and the file says otherwise",
			window)
	}
}

// The control: rta's own 24h cap still speaks in its own words when it is the
// one that bit, rather than blaming a policy that is not there.
func TestWithNoPolicyTheOwnMaximumStillSaysSo(t *testing.T) {
	setup(t)
	withPolicy(t, "")

	body, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "72h"})
	if verr != nil {
		t.Fatal(verr)
	}
	if strings.Contains(body, "team's policy") {
		t.Errorf("rta's own cap was reported as a team policy: %s", body)
	}
	if !strings.Contains(body, "maximum") {
		t.Errorf("the 24h cap did not say it applied: %s", body)
	}
}

// A refusal names the rule and the file, because a grant that silently never
// works is the failure this whole mechanism is written against.
func TestARefusalNamesTheRuleAndTheFile(t *testing.T) {
	setup(t)
	withPolicy(t, "never: [kv.get]\n")

	_, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "5m"})
	if verr == nil {
		t.Fatal("a target the policy forbids was granted")
	}
	if verr.Code != "grant.policy.refused" {
		t.Errorf("code = %q, want grant.policy.refused", verr.Code)
	}
	if !strings.Contains(verr.Message, "kv.get") || !strings.Contains(verr.Message, policy.RepoFile) {
		t.Errorf("the refusal names neither the rule nor the file: %s", verr.Message)
	}
}

// A policy ceiling looser than rta's own day never gets a chance to apply:
// parseTTL already clamps to core.MaxTTL before the policy ceiling is even
// considered, so when the policy's own maxTTL is above 24h, rta's own
// maximum is what actually bit. The message has to agree — re-asking "did
// the policy bite" against the raw, unclamped ask reaches a different answer
// than the one that decided what was stored, and blames the wrong file.
func TestALoosePolicyCeilingIsNotBlamedForRTAsOwnMaximum(t *testing.T) {
	setup(t)
	withPolicy(t, "maxTTL: 100h\n")

	body, verr := allowing(t, map[string]any{"target": "kv.get", "scope": "db", "ttl": "200h"})
	if verr != nil {
		t.Fatal(verr)
	}
	if strings.Contains(body, "team's policy") {
		t.Errorf("rta's own 24h cap was blamed on the 100h team policy, which never applied: %s", body)
	}
	if !strings.Contains(body, "maximum") {
		t.Errorf("the 24h cap did not say it applied: %s", body)
	}

	grants, gverr := core.Load()
	if gverr != nil {
		t.Fatal(gverr)
	}
	if len(grants) != 1 {
		t.Fatalf("stored %d grants, want 1", len(grants))
	}
	if window := grants[0].Expires.Sub(grants[0].Issued); window > core.MaxTTL+time.Minute {
		t.Errorf("stored window is %v, past rta's own %v maximum", window, core.MaxTTL)
	}
}

// The empty-state message is not "no grants" when grants exist on disk but
// every one of them is currently held back by the team's ceiling — that is a
// different fact, and an operator sure they issued one goes looking for it.
func TestHeldTableEmptyStateNotesCeilingSuppressedGrants(t *testing.T) {
	setup(t)
	withPolicy(t, "never: [kv.get]\n")
	now := time.Now()
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	v, verr := heldTable()
	if verr != nil {
		t.Fatal(verr)
	}
	body, ok := v.(view.Text)
	if !ok {
		t.Fatalf("held table = %s, want the empty-state Text", view.TypeOf(v))
	}
	if !strings.Contains(body.Body, "No active grants") {
		t.Errorf("body = %q, want the ordinary empty-state message first", body.Body)
	}
	if !strings.Contains(body.Body, "1 grant(s) on disk are suppressed by your team's policy") {
		t.Errorf("body = %q, want the suppression note naming the count", body.Body)
	}
	if !strings.Contains(body.Body, policy.RepoFile) {
		t.Errorf("body = %q, want the note naming the policy file", body.Body)
	}
}

// Partial suppression is the confusing case: some grants are shown, one is
// not, and the screen has to account for the gap rather than simply omitting
// the row. heldTable switches shape to say so — a Sections view splitting
// what is allowed from what the policy is holding back.
func TestHeldTablePartialSuppressionSplitsAllowedFromSuppressed(t *testing.T) {
	setup(t)
	withPolicy(t, "never: [pg.dump]\n")
	now := time.Now()
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "pg.dump", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	v, verr := heldTable()
	if verr != nil {
		t.Fatal(verr)
	}
	sections, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("held table = %s, want Sections once some grants are suppressed", view.TypeOf(v))
	}
	if len(sections.Items) != 2 {
		t.Fatalf("sections = %+v, want exactly Allowed and the policy note", sections.Items)
	}
	allowed, ok := sections.Items[0].View.(view.Table)
	if !ok || sections.Items[0].Title != "Allowed" {
		t.Fatalf("first section = %+v, want the Allowed table", sections.Items[0])
	}
	if len(allowed.Rows) != 1 || allowed.Rows[0][0] != "kv.get" {
		t.Errorf("allowed rows = %v, want only kv.get — pg.dump is suppressed", allowed.Rows)
	}
	note, ok := sections.Items[1].View.(view.Text)
	if !ok || sections.Items[1].Title != "Your team's policy" {
		t.Fatalf("second section = %+v, want the policy note", sections.Items[1])
	}
	if !strings.Contains(note.Body, "1 grant(s)") {
		t.Errorf("policy note = %q, want it to count the one suppressed grant", note.Body)
	}
}

// The detail page assembles "granted" from whatever heldTable returns, and
// heldTable can return a Sections view rather than a plain Table once the
// ceiling is suppressing something. plugin.Page.PutAs takes a view.View and
// nests it as-is — Sections composing Sections is a documented, supported
// shape — but the composition is worth pinning here: it is the one place a
// future change to either side could quietly assume "granted" is always a
// flat table and truncate or panic on this one.
func TestDetailedListHandlesHeldTableReturningSections(t *testing.T) {
	setup(t)
	withPolicy(t, "never: [kv.get]\n")
	now := time.Now()
	if verr := core.Save([]core.Grant{
		{Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(time.Hour)},
		{Target: "todo.rm", Scope: "4", Issued: now, Expires: now.Add(time.Hour)},
	}); verr != nil {
		t.Fatal(verr)
	}

	v := run(t, listH, map[string]any{"detail": true})
	page, ok := v.(view.Sections)
	if !ok {
		t.Fatalf("detailed list = %s, want Sections", view.TypeOf(v))
	}
	var granted *view.Section
	for i := range page.Items {
		if page.Items[i].Key() == "granted" {
			granted = &page.Items[i]
		}
	}
	if granted == nil {
		t.Fatal("no \"granted\" section on the detail page")
	}
	nested, ok := granted.View.(view.Sections)
	if !ok {
		t.Fatalf("granted section = %s, want the nested Allowed/policy Sections heldTable returns",
			view.TypeOf(granted.View))
	}
	if len(nested.Items) != 2 || nested.Items[0].Title != "Allowed" {
		t.Fatalf("nested sections = %+v", nested.Items)
	}
	allowedTbl, ok := nested.Items[0].View.(view.Table)
	if !ok || len(allowedTbl.Rows) != 1 || allowedTbl.Rows[0][0] != "todo.rm" {
		t.Fatalf("the allowed grant did not survive the page assembly: %+v", nested.Items[0].View)
	}
	// The reach tiers beside it have to survive too — a page that lost them
	// the moment "granted" became a Sections would answer only half of
	// "what can an agent do here".
	for _, id := range []string{"default", "allow-write", "grant"} {
		found := false
		for _, item := range page.Items {
			if item.Key() == id {
				found = true
			}
		}
		if !found {
			t.Errorf("reach tier %q missing from the page once granted was a Sections view", id)
		}
	}
}
