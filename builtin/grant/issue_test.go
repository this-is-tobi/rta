package grant

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// roleSetup isolates the grant file, the config and the policy walk, and
// opens one presence record so an omitted --agent resolves to "test".
func roleSetup(t *testing.T) (configDir, repo string) {
	t.Helper()
	setup(t)
	configDir, repo = t.TempDir(), t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(configDir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(repo)
	return configDir, repo
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func ownRole(t *testing.T, configDir, body string) {
	t.Helper()
	writeFile(t, filepath.Join(configDir, "config.yaml"), "roles:\n"+body)
}

func teamPolicy(t *testing.T, repo, body string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, ".rta-policy.yaml"), body)
}

// issueReq is a CLI request with --yes off, because the team-role rule is
// about --yes and this package's req() helper passes it on.
func issueReq(values map[string]any, dryRun, yes bool) plugin.Request {
	return plugin.NewRequest(values, dryRun, yes).WithSurface(plugin.SurfaceCLI)
}

func issueH(_ context.Context, req plugin.Request) (view.View, error) {
	return runIssue(req, catalog, builtIn)
}

func issue(t *testing.T, values map[string]any) view.Sections {
	t.Helper()
	v, err := issueH(context.Background(), issueReq(values, false, false))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return v.(view.Sections)
}

func issueErr(t *testing.T, req plugin.Request) *view.Error {
	t.Helper()
	_, err := issueH(context.Background(), req)
	var verr *view.Error
	if err == nil || !asError(err, &verr) {
		t.Fatalf("issue: %v, want a refusal", err)
	}
	return verr
}

func receipt(s view.Sections) view.KeyValue { return s.Items[0].View.(view.KeyValue) }

func plan(s view.Sections) view.Table { return s.Items[1].View.(view.Table) }

func pair(kv view.KeyValue, key string) string {
	for _, p := range kv.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return ""
}

func standing(t *testing.T) []core.Grant {
	t.Helper()
	all, verr := core.Load()
	if verr != nil {
		t.Fatal(verr)
	}
	return all
}

// One word issues every line to the one agent this machine knows, under
// the role's name; list shows the role; revoke --role takes it all back
// and says, computed, what still stands.
func TestARoleIsIssuedWholeAndTakenBackWhole(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    ttl: 4h\n    grants:\n      - kv.get db-password\n      - kv.env --note exports\n")
	s := issue(t, map[string]any{"role": "dev"})
	r := receipt(s)
	if pair(r, "agent") != "test" || pair(r, "grants") != "2" || !strings.HasPrefix(pair(r, "role"), "dev — yours") {
		t.Fatalf("receipt = %+v", r.Pairs)
	}
	if got := pair(r, "take back"); got != "rta grant revoke --role dev --agent test" {
		t.Fatalf("take back = %q", got)
	}
	all := standing(t)
	if len(all) != 2 {
		t.Fatalf("%d grants, want 2", len(all))
	}
	notes := map[string]string{}
	for _, g := range all {
		if g.Role != "dev" || g.Agent != "test" {
			t.Fatalf("grant %+v is not stamped with the role", g)
		}
		if want := 4 * time.Hour; g.Expires.Sub(g.Issued).Round(time.Minute) != want {
			t.Fatalf("%s lasts %s, want the role's %s", g.Target, g.Expires.Sub(g.Issued), want)
		}
		notes[g.Target] = g.Note
	}
	if notes["kv.get"] != "" || notes["kv.env"] != "exports" {
		t.Fatalf("notes = %v: a line's own note and nothing invented", notes)
	}

	lv := run(t, listH, nil)
	tbl := listed(t, lv)
	if tbl.Columns[2].Name != "Role" && tbl.Columns[3].Name != "Role" {
		t.Fatalf("no Role column: %+v", tbl.Columns)
	}
	if only, ok := run(t, listH, map[string]any{"role": "nope"}).(view.Text); !ok || !strings.Contains(only.Body, "No grant is standing") {
		t.Fatalf("--role nope listed something: %+v", only)
	}

	rv := run(t, runRevoke, map[string]any{"role": "dev"})
	body := rv.(view.Text).Body
	if !strings.Contains(body, "2") || !strings.Contains(body, "nothing else stands") {
		t.Fatalf("revoke said %q", body)
	}
	if len(standing(t)) != 0 {
		t.Fatal("grants survived their role's revocation")
	}
}

func TestIssuingAgainRefreshesRatherThanDoubles(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants: [kv.get, kv.env]\n")
	issue(t, map[string]any{"role": "dev"})
	r := receipt(issue(t, map[string]any{"role": "dev"}))
	if got := pair(r, "replaced"); !strings.Contains(got, "refreshed") {
		t.Fatalf("second issue said replaced = %q", got)
	}
	if len(standing(t)) != 2 {
		t.Fatalf("%d grants after issuing twice, want 2", len(standing(t)))
	}
}

// A role line equal to a grant issued by hand takes that grant with it —
// Issue keeps one row per target, record, connection and agent — so the
// receipt says so before anyone assumes the hand grant survives, and the
// revocation says what is really left rather than promising.
func TestALineThatReplacesAHandGrantSaysSo(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.get db-password\n      - kv.env\n")
	byHand := core.Grant{Target: "kv.get", Scope: "db-password", Agent: "test", Issued: time.Now(),
		Expires: time.Now().Add(time.Hour), From: core.FromCommand}
	if verr := core.Issue(byHand, true); verr != nil {
		t.Fatal(verr)
	}
	s := issue(t, map[string]any{"role": "dev"})
	if got := pair(receipt(s), "replaced"); !strings.HasPrefix(got, "1") {
		t.Fatalf("replaced = %q, want the hand grant counted", got)
	}
	var replaces string
	for _, row := range plan(s).Rows {
		if row[0] == "kv.get" {
			replaces = row[len(row)-1]
		}
	}
	if !strings.HasPrefix(replaces, "by hand") {
		t.Fatalf("plan says kv.get replaces %q, want the hand grant named", replaces)
	}
	rv := run(t, runRevoke, map[string]any{"role": "dev", "agent": "test"})
	if body := rv.(view.Text).Body; !strings.Contains(body, "nothing else stands for test") {
		t.Fatalf("revoke said %q; the hand grant went with the role and the sentence must say so", body)
	}
}

func TestTheWindowIsTheFlagThenTheRoleThenTwelveHours(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  a:\n    grants: [kv.env]\n  b:\n    ttl: 2h\n    grants:\n      - kv.env --ttl 30m\n      - kv.get --ttl 8h\n")
	lasting := func(target string) time.Duration {
		for _, g := range standing(t) {
			if g.Target == target {
				return g.Expires.Sub(g.Issued).Round(time.Minute)
			}
		}
		t.Fatalf("no grant on %s", target)
		return 0
	}
	issue(t, map[string]any{"role": "a"})
	if got := lasting("kv.env"); got != 12*time.Hour {
		t.Fatalf("default = %s, want 12h", got)
	}
	issue(t, map[string]any{"role": "a", "ttl": "1h"})
	if got := lasting("kv.env"); got != time.Hour {
		t.Fatalf("--ttl = %s, want 1h", got)
	}
	issue(t, map[string]any{"role": "b"})
	if got := lasting("kv.env"); got != 30*time.Minute {
		t.Fatalf("a line's shorter ttl = %s, want 30m", got)
	}
	if got := lasting("kv.get"); got != 2*time.Hour {
		t.Fatalf("a line's longer ttl = %s, want the role's 2h", got)
	}
}

func TestOneBadLineRefusesTheWholeRole(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.env\n      - nosuch.thing\n")
	verr := issueErr(t, issueReq(map[string]any{"role": "dev"}, false, false))
	if !strings.Contains(verr.Message, `role "dev", line "nosuch.thing"`) {
		t.Fatalf("issue = %v, want the line named", verr)
	}
	if len(standing(t)) != 0 {
		t.Fatal("a grant was written before the bad line was found")
	}
	ownRole(t, configDir, "  dev:\n    grants: [todo.list]\n")
	if verr := issueErr(t, issueReq(map[string]any{"role": "dev"}, false, false)); verr.Code != "grant.needless" {
		t.Fatalf("a read in a role = %v, want refused as needless", verr)
	}
}

func TestADryRunOfARoleWritesNothing(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants: [kv.env, kv.get]\n")
	v, err := issueH(context.Background(), issueReq(map[string]any{"role": "dev"}, true, false))
	if err != nil {
		t.Fatal(err)
	}
	body := v.(view.Sections).Items[0].View.(view.Text).Body
	if !strings.HasPrefix(body, "would issue dev to test: 2 grants") {
		t.Fatalf("dry run said %q", body)
	}
	if len(standing(t)) != 0 {
		t.Fatal("a dry run wrote grants")
	}
}

// A repository's role is somebody else's list: issued at the command line
// only, and with the guard off only after --yes says the lines were read.
func TestATeamRoleIsIssuedAtTheCommandLineOnly(t *testing.T) {
	_, repo := roleSetup(t)
	teamPolicy(t, repo, "roles:\n  dev:\n    grants: [kv.env]\n")
	verr := issueErr(t, issueReq(map[string]any{"role": "dev"}, false, false))
	if verr.Code != "grant.role.unread" || !strings.Contains(verr.Hint, "rta grant roles dev") {
		t.Fatalf("team role without --yes = %v", verr)
	}
	tui := plugin.NewRequest(map[string]any{"role": "dev"}, false, true).WithSurface(plugin.SurfaceTUI)
	if verr := issueErr(t, tui); verr.Code != "grant.role.team" {
		t.Fatalf("team role from a form = %v, want refused even with yes", verr)
	}
	if len(standing(t)) != 0 {
		t.Fatal("grants were written without the acknowledgement")
	}
	v, err := issueH(context.Background(), issueReq(map[string]any{"role": "dev"}, false, true))
	if err != nil {
		t.Fatal(err)
	}
	if got := pair(receipt(v.(view.Sections)), "role"); !strings.Contains(got, "the team's") {
		t.Fatalf("role = %q, want it named as the team's", got)
	}
}

// A file the operator named with RTA_POLICY is theirs, whatever it holds.
func TestARoleFromAFileTheOperatorNamedIsTheirs(t *testing.T) {
	_, repo := roleSetup(t)
	own := filepath.Join(repo, "mine.yaml")
	writeFile(t, own, "roles:\n  dev:\n    grants: [kv.env]\n")
	t.Setenv("RTA_POLICY", own)
	s := issue(t, map[string]any{"role": "dev"})
	if got := pair(receipt(s), "role"); !strings.Contains(got, "yours") {
		t.Fatalf("role = %q, want it named as yours", got)
	}
}

func TestRolesSayTheWindowEachWillReallyGet(t *testing.T) {
	configDir, repo := roleSetup(t)
	teamPolicy(t, repo, "maxTTL: 1h\n")
	ownRole(t, configDir, "  dev:\n    ttl: 8h\n    grants: [kv.env]\n  quick:\n    ttl: 30m\n    grants: [kv.env]\n")
	v := run(t, runRoles, nil)
	rows := v.(view.Table).Rows
	if len(rows) != 2 || !strings.HasPrefix(rows[0][2], "8h, capped to 1h by ") || rows[1][2] != "30m" {
		t.Fatalf("roles = %+v", rows)
	}
	one := run(t, runRoles, map[string]any{"role": "quick"})
	if len(one.(view.Table).Rows) != 1 {
		t.Fatalf("roles quick = %+v", one)
	}
}

func TestRenewMovesAWholeRole(t *testing.T) {
	configDir, _ := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    ttl: 1h\n    grants: [kv.env, kv.get]\n")
	issue(t, map[string]any{"role": "dev"})
	byHand := core.Grant{Target: "todo.rm", Agent: "test", Issued: time.Now(),
		Expires: time.Now().Add(time.Hour), From: core.FromCommand}
	if verr := core.Issue(byHand, true); verr != nil {
		t.Fatal(verr)
	}
	v := run(t, runRenew, map[string]any{"role": "dev", "ttl": "3h"})
	if body := v.(view.Text).Body; !strings.Contains(body, "renewed 2 grant") {
		t.Fatalf("renew --role said %q", body)
	}
	for _, g := range standing(t) {
		left := time.Until(g.Expires).Round(time.Minute)
		switch {
		case g.Role == "dev" && left < 2*time.Hour+50*time.Minute:
			t.Fatalf("%s was not renewed: %s left", g.Target, left)
		case g.Role == "" && left > time.Hour+time.Minute:
			t.Fatalf("the hand grant was renewed along with the role: %s left", left)
		}
	}
}
