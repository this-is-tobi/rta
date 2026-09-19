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
	if got[0].scope != "this project (.mcp.json)" || got[0].as != "claude" {
		t.Errorf("project = %+v", got[0])
	}
	if got[1].scope != "this directory only" || got[1].as != "claude-here" {
		t.Errorf("directory = %+v", got[1])
	}
	userWide := `{"mcpServers":{"rta":{"command":"rta","args":["mcp","serve"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(userWide), 0o600); err != nil {
		t.Fatal(err)
	}
	got = claudeRegistrations(home, t.TempDir())
	if len(got) != 1 || got[0].scope != "every project" || got[0].as != "" {
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
	for _, row := range clientRows(false, "v0.22.0") {
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
	for _, row := range clientRows(false, "v0.22.0") {
		if row[0] == "agents connected" && row[1] == "warn" {
			t.Errorf("warned about a server on this very build: %s", row[2])
		}
	}
}
