package app

import (
	"os"
	"path/filepath"
	"testing"
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
