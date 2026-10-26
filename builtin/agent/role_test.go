package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/agentlog"
	"github.com/this-is-tobi/rta/internal/consent"
	core "github.com/this-is-tobi/rta/internal/grant"
	presence "github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func catalog() []plugin.Capability {
	return []plugin.Capability{
		{ID: "kv.get", Summary: "reveal", Safety: plugin.Write, Scope: "key",
			Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}},
		{ID: "kv.set", Summary: "store", Safety: plugin.Write},
		{ID: "kv.rename", Summary: "rename", Safety: plugin.Write, Scope: "key",
			Inputs: []plugin.Field{{Name: "key", Type: plugin.String, Positional: true}}},
		{ID: "note.add", Summary: "add", Safety: plugin.Write},
		{ID: "http.get", Summary: "fetch", Safety: plugin.Write, Scope: "url",
			Inputs: []plugin.Field{{Name: "url", Type: plugin.String, Positional: true}}},
	}
}

func builtIn(string) (string, bool) { return "", true }

// roleSetup isolates the config and the policy walk beside the data dir.
func roleSetup(t *testing.T) (configDir string) {
	t.Helper()
	isolate(t)
	configDir = t.TempDir()
	t.Setenv("RTA_CONFIG", filepath.Join(configDir, "config.yaml"))
	t.Setenv("RTA_POLICY", "")
	t.Chdir(t.TempDir())
	return configDir
}

func ownRole(t *testing.T, configDir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("roles:\n"+body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendRow(t *testing.T, e agentlog.Entry) {
	t.Helper()
	if err := agentlog.Append(e); err != nil {
		t.Fatal(err)
	}
}

// The record names the role a call spent, so "what did dev do today" is one
// flag rather than a join of the roster against the log.
func TestTheLogFiltersAndShowsTheRole(t *testing.T) {
	roleSetup(t)
	appendRow(t, agentlog.Entry{Cap: "kv.get", Tool: "kv_get", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Standing, Role: "dev", RoleIssued: "2026-09-06T09:00:00Z"})
	appendRow(t, agentlog.Entry{Cap: "note.add", Tool: "note_add", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Standing})
	appendRow(t, agentlog.Entry{Cap: "kv.set", Tool: "kv_set", Agent: "claude", Outcome: agentlog.Ran, Auth: agentlog.Standing, Role: "ops, dev"})
	v, err := run(t, "agent.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	tbl := v.(view.Table)
	col := -1
	for i, c := range tbl.Columns {
		if c.Name == "role" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no role column: %+v", tbl.Columns)
	}
	if len(tbl.Rows) != 3 || tbl.Rows[0][col] != "dev" || tbl.Rows[1][col] != "—" {
		t.Fatalf("rows = %+v", tbl.Rows)
	}
	v, err = run(t, "agent.log", map[string]any{"role": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if rows := v.(view.Table).Rows; len(rows) != 2 {
		t.Fatalf("--role dev kept %d rows, want the two dev covered", len(rows))
	}
}

func TestTheOverviewSaysWhichRolesStand(t *testing.T) {
	roleSetup(t)
	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "roles in force"); got != "" {
		t.Fatalf("roles in force = %q on a clean machine, where the compact answer leaves the quiet line out", got)
	}
	if got := valueOf(t, activityOf(t, plugin.SurfaceCLI), "roles in force"); got != "none" {
		t.Fatalf("roles in force = %q on a clean machine's detail page, which says none is", got)
	}
	if verr := core.Issue(core.Grant{Target: "kv.set", Agent: "claude", Role: "dev", Issued: time.Now(), Expires: time.Now().Add(2 * time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	v, err = run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "roles in force"); !strings.HasPrefix(got, "dev for claude — 1 grant") {
		t.Fatalf("roles in force = %q", got)
	}
}

// A grants file rta cannot read is not a machine with no roles issued.
// Before this, RolesInForce folded core.Load's error into "" the same way a
// clean machine reports, and rolesLine turned that into "none" — the
// dashboard telling an operator no standing role is issued when the truth is
// that rta could not check.
func TestOverviewRolesIsUnreadableWhenTheGrantsFileCannotBeRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	roleSetup(t)
	if verr := core.Issue(core.Grant{Target: "kv.set", Agent: "claude", Role: "dev",
		Issued: time.Now(), Expires: time.Now().Add(2 * time.Hour)}, true); verr != nil {
		t.Fatal(verr)
	}
	if err := os.Chmod(core.Path(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(core.Path(), 0o600) })

	v, err := run(t, "agent.overview", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pairValue(v, "roles in force"); !strings.HasPrefix(got, "could not be read") {
		t.Fatalf("roles in force = %q, want it to say the grants file could not be read", got)
	}
}

// A call naming two records is covered by a role only when every record is:
// by one line naming no record, or by a line for each. Every kv.rename names
// two, its key and where it goes, and a role was held to covering a call
// naming exactly one, so no role was ever offered for a rename, even one
// with a line for each of its records.
func TestARoleCoversACallNamingSeveralRecordsLineByLine(t *testing.T) {
	configDir := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.get db-password\n"+
		"      - kv.rename db-password\n      - kv.rename db-password-old\n"+
		"  half:\n    grants:\n      - kv.rename db-password\n")
	r := park(t, "kv.rename", "db-password", "db-password-old")
	got := roleHint(plugin.SurfaceCLI, r)
	if !strings.HasPrefix(got, "lines 2 and 3 of dev — ") || !strings.Contains(got, "--role dev") {
		t.Errorf("role hint = %q, want dev's two rename lines named", got)
	}

	// Named in the order the role lists them, whichever record each covers.
	configDir = roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.rename db-password-old\n"+
		"      - kv.rename db-password\n")
	got = roleHint(plugin.SurfaceCLI, park(t, "kv.rename", "db-password", "db-password-old"))
	if !strings.HasPrefix(got, "lines 1 and 2 of dev — ") {
		t.Errorf("role hint = %q, want the lines in the role's order", got)
	}

	configDir = roleSetup(t)
	ownRole(t, configDir, "  half:\n    grants:\n      - kv.rename db-password\n")
	if got := roleHint(plugin.SurfaceCLI, park(t, "kv.rename", "db-password", "db-password-old")); got != "" {
		t.Errorf("a role covering one of the two records was offered: %q", got)
	}
}

// A role line naming a folder covers a call on a record under it, as the
// grant it issues would: `kv.get prod/` was held to the record "prod/"
// exactly, so a call on prod/db was never offered the role that covers it.
// By the gate's own folder rule, so the boundary is the gate's too: the
// slash is the boundary, and a climb out of the folder is never covered.
func TestAFolderRoleLineCoversACallOnARecordUnderIt(t *testing.T) {
	configDir := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.get prod/\n")
	if got := roleHint(plugin.SurfaceCLI, park(t, "kv.get", "prod/db")); !strings.HasPrefix(got, "line 1 of dev — ") {
		t.Errorf("role hint for prod/db = %q, want dev's folder line offered", got)
	}
	for _, record := range []string{"production/x", "prod", "prod/../secret"} {
		if got := roleHint(plugin.SurfaceCLI, park(t, "kv.get", record)); got != "" {
			t.Errorf("a line on prod/ was offered for %q: %q", record, got)
		}
	}
}

// A parked call whose capability a role's line covers says so on its page,
// and can be answered with the whole role: one passphrase for the day
// instead of one --ttl answer per capability as the calls arrive.
func TestAParkedCallCanBeAnsweredWithItsRole(t *testing.T) {
	configDir := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    ttl: 2h\n    grants:\n      - kv.set\n      - kv.get db-password\n")
	if err := presence.Start(presence.Record{ID: presence.NewID(), Agent: "claude", Since: time.Now(), PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	p, err := consent.Ask(consent.Call{Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"}, Agent: "claude",
		Args: map[string]any{"key": "db-password"}, Why: "no active grant"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)

	sv, err := run(t, "agent.show", map[string]any{"id": p.Request.ID})
	if err != nil {
		t.Fatal(err)
	}
	page := sv.(view.Sections).Items[0].View.(view.KeyValue)
	if got := pairValue(page, "role"); !strings.HasPrefix(got, "line 2 of dev") || !strings.Contains(got, "--role dev") {
		t.Fatalf("show's role hint = %q", got)
	}

	if _, err := run(t, "agent.allow", map[string]any{"id": p.Request.ID, "role": "dev", "ttl": "1h"}); err == nil {
		t.Fatal("--role and --ttl together were accepted")
	}
	av, err := run(t, "agent.allow", map[string]any{"id": p.Request.ID, "role": "dev"})
	if err != nil {
		t.Fatal(err)
	}
	answer := av.(view.Sections).Items[0].View.(view.KeyValue)
	if got := pairValue(answer, "for"); !strings.Contains(got, "role dev issued to claude") {
		t.Fatalf("answer = %+v", answer.Pairs)
	}
	grants, verr := core.Load()
	if verr != nil || len(grants) != 2 {
		t.Fatalf("grants after answering with the role: %+v, %v", grants, verr)
	}
	if a := p.Wait(context.Background()); !a.Answered || !a.Allowed {
		t.Fatalf("the call itself was not decided: %+v", a)
	}
}

// parkFor parks a kv.get on db-password for a connected agent, the call a
// role's line answers.
func parkFor(t *testing.T, agent string) consent.Request {
	t.Helper()
	if err := presence.Start(presence.Record{ID: presence.NewID(), Agent: agent, Since: time.Now(), PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	p, err := consent.Ask(consent.Call{Cap: "kv.get", Safety: "write", Scopes: []string{"db-password"}, Agent: agent,
		Args: map[string]any{"key": "db-password"}, Why: "no active grant"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p.Request
}

// A call the team's ceiling forbids is refused before its role is issued, a
// preview included. The ceiling was asked about the call only after every
// line of the role had been written, and away from a terminal or with --yes
// that was the only time it was asked: the answer was the policy refusal
// alone, and the role's grants stood behind it with no receipt and no line
// to take them back.
func TestARoleIsNotIssuedForACallTheCeilingForbids(t *testing.T) {
	configDir := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.set\n      - note.add\n")
	withPolicy(t, "never:\n  - kv.get\n")
	r := parkFor(t, "claude")

	allow := capability(t, "agent.allow")
	for _, dry := range []bool{true, false} {
		values := plugin.Resolve(allow, plugin.Inputs{Caller: map[string]any{"id": r.ID, "role": "dev"}})
		_, err := allow.Run(context.Background(), plugin.NewRequest(values, dry, true).WithSurface(plugin.SurfaceCLI))
		var ve *view.Error
		if !errors.As(err, &ve) || ve.Code != "grant.policy.refused" {
			t.Fatalf("dry run %v: answered with %v, want the policy refusal", dry, err)
		}
	}
	if grants, verr := core.Load(); verr != nil || len(grants) != 0 {
		t.Fatalf("grants after the refusal: %+v, %v — want none issued", grants, verr)
	}
	if _, ok := consent.Find(r.ID); !ok {
		t.Fatal("the parked request was consumed by a refused answer")
	}
}

// A role issued for a call that then cannot be released is a failure that
// names the role, not a bare one and not a success. The answer is decided
// after the role is issued, which can take a passphrase and as long as the
// person takes to type it, and a call's deadline can pass meanwhile: the
// answer was the bare agent.allow.failed, and the role stood without a word.
// Answered with a view instead, the command exited 0, and a script's next step
// ran for a call that was never released.
func TestARoleIssuedForACallThatCouldNotBeReleasedIsStillReported(t *testing.T) {
	configDir := roleSetup(t)
	ownRole(t, configDir, "  dev:\n    grants:\n      - kv.set\n      - kv.get db-password\n")
	r := parkFor(t, "claude")
	decided := decide
	decide = func(string, bool, string) error { return errors.New("no request \"" + r.ID + "\" is waiting") }
	t.Cleanup(func() { decide = decided })

	v, err := run(t, "agent.allow", map[string]any{"id": r.ID, "role": "dev"})
	var ve *view.Error
	if !errors.As(err, &ve) || ve.Code != "agent.allow.unreleased" {
		t.Fatalf("answered %+v, %v — want agent.allow.unreleased", v, err)
	}
	for _, want := range []string{"kv.get db-password was not released", "no request", "role dev was issued to claude and stands"} {
		if !strings.Contains(ve.Message, want) {
			t.Errorf("message = %q, want it to say %q", ve.Message, want)
		}
	}
	if want := "rta grant revoke --role dev --agent claude"; !strings.Contains(ve.Hint, want) {
		t.Errorf("hint = %q, want the call that takes the role back, %q", ve.Hint, want)
	}
	if grants, verr := core.Load(); verr != nil || len(grants) != 2 {
		t.Fatalf("grants = %+v, %v — want the role's two", grants, verr)
	}
}
