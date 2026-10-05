package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentsession "github.com/this-is-tobi/rta/internal/session"
)

func TestClaudeRegistrationsTellTheThreeScopesApart(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	if got := claudeRegistrations(home, dir); len(got) != 0 {
		t.Fatalf("nothing configured = %v", got)
	}
	project := `{"mcpServers":{"rta":{"command":"/usr/local/bin/rta","args":["mcp","serve","--as","claude"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	user := `{"mcpServers":{"other":{"command":"npx","args":["x"]}},"projects":{"` + dir + `":{"mcpServers":{"rta":{"type":"stdio","command":"/Users/me/go/bin/rta","args":["mcp","serve","--as","claude-here"]}}}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(user), 0o600); err != nil {
		t.Fatal(err)
	}
	got := claudeRegistrations(home, dir)
	if len(got) != 2 {
		t.Fatalf("registrations = %+v, want the project file and the directory entry", got)
	}
	if got[0].scope != scopeProject || got[0].scope.where() != "this project (.mcp.json)" || got[0].as != "claude" {
		t.Errorf("project = %+v", got[0])
	}
	if got[1].scope != scopeLocal || got[1].scope.where() != "this directory only" || got[1].as != "claude-here" ||
		got[1].command != "/Users/me/go/bin/rta" || strings.Join(got[1].args, " ") != "mcp serve --as claude-here" {
		t.Errorf("directory = %+v", got[1])
	}
	userWide := `{"mcpServers":{"rta":{"command":"rta","args":["mcp","serve"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(userWide), 0o600); err != nil {
		t.Fatal(err)
	}
	got = claudeRegistrations(home, t.TempDir())
	if len(got) != 1 || got[0].scope != scopeUser || got[0].scope.where() != "every project" || got[0].as != "" {
		t.Errorf("user-wide without --as = %+v", got)
	}
}

// A server keeps the build it started with. Claude Code holds one open for
// days, so after an upgrade the process answering calls can be decidings by
// rules this page no longer reads — and the failure that produces is silent:
// a grant issued from the file, listed as healthy, and refused by a server
// whose view of the connection predates it. Nothing named the difference, so
// the operator re-issued the grant they already had.
func TestAConnectedServerOnAnotherBuildIsCalledOut(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	now := time.Now()
	for _, r := range []agentsession.Record{
		{ID: agentsession.NewID(), Agent: "claude", Since: now, PID: 4242, Version: "v0.22.0"},
		{ID: agentsession.NewID(), Agent: "claude", Since: now, PID: 17188, Version: "v0.21.1"},
		{ID: agentsession.NewID(), Agent: "cursor", Since: now, PID: 22451},
	} {
		if err := agentsession.Start(r); err != nil {
			t.Fatal(err)
		}
	}
	var warn string
	for _, row := range clientRows(false, "v0.22.0", false) {
		if row[0] == "agents connected" && row[1] == "warn" {
			warn = row[2]
		}
	}
	if warn == "" {
		t.Fatal("no warning about the servers running another build")
	}
	// The count and its verb, in a sentence. This read "2 are on running a
	// build this is not" until format.Count and format.Plural were told
	// apart: internal/app's own plural printed the number and builtin/grant's
	// did not, under one name and one signature.
	if !strings.HasPrefix(warn, "2 servers are running a build this is not") {
		t.Errorf("the warning does not open as a sentence: %s", warn)
	}
	for _, want := range []string{"17188", "v0.21.1", "22451"} {
		if !strings.Contains(warn, want) {
			t.Errorf("the warning does not name %q: %s", want, warn)
		}
	}
	if strings.Contains(warn, "4242") {
		t.Errorf("the server on this build was called stale: %s", warn)
	}
}

// And says nothing when every open server is this build, since a warning
// that fires on the ordinary case is one people learn to scroll past.
func TestServersOnThisBuildDrawNoWarning(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := agentsession.Start(agentsession.Record{
		ID: agentsession.NewID(), Agent: "claude", Since: time.Now(), PID: 4242, Version: "v0.22.0",
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range clientRows(false, "v0.22.0", false) {
		if row[0] == "agents connected" && row[1] == "warn" {
			t.Errorf("warned about a server on this very build: %s", row[2])
		}
	}
}

// A session store rta cannot read is not a machine with nothing connected.
// Before this, agentcap.Connected()'s error had no way out of the function —
// clientRows saw n==0 whether the store was empty or unreadable, and told
// the operator "no client has an rta server open" during exactly the
// incident where that line matters most.
func TestAgentsConnectedWarnsWhenItCannotCheck(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := agentsession.Start(agentsession.Record{
		ID: agentsession.NewID(), Agent: "claude", Since: time.Now(), PID: 4242,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(agentsession.Dir(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(agentsession.Dir(), 0o700) })

	var row [3]string
	for _, r := range clientRows(false, "v0.22.0", false) {
		if r[0] == "agents connected" {
			row = r
		}
	}
	if row[1] != "warn" || !strings.Contains(row[2], "could not check") {
		t.Fatalf("agents connected row = %+v, want a warn row saying it could not check", row)
	}
}

// The row for Claude Code names the command that fixes what it found, not
// claude's own long spelling of it, and stops nagging about a directory-only
// registration once an every-project one stands beside it.
func TestTheClaudeRowsNameTheCommandThatFixesThem(t *testing.T) {
	rows := claudeRows(true, nil)
	if len(rows) != 1 || rows[0][1] != "info" || !strings.Contains(rows[0][2], "`rta mcp install claude --global`") {
		t.Fatalf("not registered = %v, want one info row naming rta mcp install claude --global", rows)
	}
	if rows := claudeRows(false, nil); len(rows) != 0 {
		t.Errorf("no claude and nothing registered = %v, want no row about a client that is not here", rows)
	}

	local := claudeRegistration{scope: scopeLocal, as: "claude", name: "rta"}
	rows = claudeRows(true, []claudeRegistration{local})
	if len(rows) != 1 || rows[0][1] != "info" || !strings.Contains(rows[0][2], "`rta mcp install claude --global`") ||
		strings.Contains(rows[0][2], "claude mcp add") {
		t.Errorf("directory only = %v, want the nag with rta's own command", rows)
	}

	user := claudeRegistration{scope: scopeUser, as: "claude", name: "rta"}
	rows = claudeRows(true, []claudeRegistration{user, local})
	for _, r := range rows {
		if r[1] != "ok" || strings.Contains(r[2], "a session opened elsewhere") {
			t.Errorf("both scopes, same name = %v, want ok rows and no nag", r)
		}
	}

	elsewhere := local
	elsewhere.as = "claude-here"
	rows = claudeRows(true, []claudeRegistration{user, elsewhere})
	if len(rows) != 2 || rows[1][1] != "info" || !strings.Contains(rows[1][2], "overrides the every-project registration") {
		t.Errorf("both scopes, two names = %v, want the override said", rows)
	}

	rows = claudeRows(true, []claudeRegistration{{scope: scopeUser, name: "rta"}})
	if len(rows) != 1 || rows[0][1] != "warn" || !strings.Contains(rows[0][2], "refuses to start without one") {
		t.Errorf("no --as = %v, want a warning: rta mcp serve refuses to start without a name", rows)
	}
}

// Every other client on the machine is asked whether rta is registered with
// it, by the reader `rta audit clients` uses; a client that is not here earns
// no row.
func TestEveryOtherClientOnTheMachineIsAskedWhetherRtaIsRegistered(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	home, wd := t.TempDir(), t.TempDir()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers":{"rta":{"command":"/opt/homebrew/bin/rta","args":["mcp","serve","--as","cursor"]}}}`)
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(home, ".copilot", "mcp-config.json"), "{ not json")

	rows := map[string][3]string{}
	for _, r := range otherClientRows(home, wd) {
		rows[r[0]] = r
	}
	if r := rows["cursor"]; r[1] != "ok" || !strings.Contains(r[2], "starts rta as cursor") || !strings.Contains(r[2], "mcp.json") {
		t.Errorf("cursor = %v, want a registered row naming the name and the file", r)
	}
	if r := rows["gemini"]; r[1] != "info" || !strings.Contains(r[2], "`rta mcp install gemini`") {
		t.Errorf("gemini = %v, want installed and not registered, with the command", r)
	}
	if r := rows["copilot"]; r[1] != "warn" || !strings.Contains(r[2], "is not known") {
		t.Errorf("copilot = %v, want a warning that it could not tell", r)
	}
	for _, absent := range []string{"codex", "vscode", "claude"} {
		if _, ok := rows[absent]; ok {
			t.Errorf("a row for %s, which is not on this machine: %v", absent, rows[absent])
		}
	}
}
