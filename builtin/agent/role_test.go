package agent

import (
	"context"
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
		{ID: "note.add", Summary: "add", Safety: plugin.Write},
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
	if got := pairValue(v, "roles in force"); got != "none" {
		t.Fatalf("roles in force = %q on a clean machine", got)
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
