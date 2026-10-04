package audit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeClientFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func vscodeMCPPath(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json")
	}
	return filepath.Join(home, ".config", "Code", "User", "mcp.json")
}

// `rta doctor` asks every client's file whether rta is in it, with the walk
// the audit grades them by: a declaration is found by its shape, wherever it
// sits, in the JSON clients and in Codex's TOML, and in the JSONC that VS Code
// lets an operator annotate.
func TestRegistrationsFindRtaInEveryClientsFile(t *testing.T) {
	home, wd := t.TempDir(), t.TempDir()
	rta := `{"command": "/opt/homebrew/bin/rta", "args": ["mcp", "serve", "--as", "%s"]}`
	writeClientFile(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers": {"rta": `+strings.Replace(rta, "%s", "cursor", 1)+`, "other": {"command": "npx", "args": ["x"]}}}`)
	writeClientFile(t, filepath.Join(home, ".gemini", "settings.json"),
		`{"mcpServers": {"mine": `+strings.Replace(rta, "%s", "gemini", 1)+`}}`)
	writeClientFile(t, filepath.Join(home, ".codex", "config.toml"),
		"[mcp_servers.rta]\ncommand = \"/opt/homebrew/bin/rta\"\nargs = [\"mcp\", \"serve\", \"--as\", \"codex\"]\n")
	writeClientFile(t, vscodeMCPPath(home),
		"{\n  // added by hand\n  \"servers\": {\n    \"rta\": "+strings.Replace(rta, "%s", "vscode", 1)+",\n  },\n}\n")
	writeClientFile(t, filepath.Join(wd, ".mcp.json"),
		`{"mcpServers": {"rta": `+strings.Replace(rta, "%s", "claude", 1)+`}}`)

	found, unread := Registrations(home, wd)
	if len(unread) != 0 {
		t.Errorf("unread = %v, want every file read", unread)
	}
	byAs := map[string]Registration{}
	for _, r := range found {
		byAs[r.As()] = r
	}
	for as, label := range map[string]string{
		"cursor": "Cursor", "gemini": "Gemini CLI", "codex": "Codex CLI", "vscode": "VS Code",
		"claude": "Claude Code (this project)",
	} {
		r, ok := byAs[as]
		if !ok {
			t.Errorf("no registration as %s in %v", as, found)
			continue
		}
		if r.Label != label || r.Command != "/opt/homebrew/bin/rta" {
			t.Errorf("registration as %s = %+v, want label %q and the command", as, r, label)
		}
	}
	if r := byAs["gemini"]; r.Name != "mine" {
		t.Errorf("the name a declaration sits under is %q, want mine", r.Name)
	}
	if len(found) != 5 {
		t.Errorf("found %d registrations, want 5 and not the unrelated npx server: %+v", len(found), found)
	}
}

// A file that exists and cannot be asked is not a file that holds nothing, and
// a client without the file is not "unreadable" at all.
func TestRegistrationsSayWhichFilesTheyCouldNotRead(t *testing.T) {
	home, wd := t.TempDir(), t.TempDir()
	writeClientFile(t, filepath.Join(home, ".cursor", "mcp.json"), "{ this is not json")
	found, unread := Registrations(home, wd)
	if len(found) != 0 {
		t.Errorf("found %+v in a file that does not parse", found)
	}
	if len(unread) != 1 || unread[0].Label != "Cursor" || !strings.HasSuffix(unread[0].File, "mcp.json") {
		t.Errorf("unread = %+v, want only the cursor file", unread)
	}
}

// The binary is whatever the operator's package or build named it; what makes a
// declaration rta's is the server it starts.
func TestARegistrationIsFoundByTheServerItStarts(t *testing.T) {
	home := t.TempDir()
	writeClientFile(t, filepath.Join(home, ".cursor", "mcp.json"),
		`{"mcpServers": {"dev": {"command": "/work/rta-dev", "args": ["mcp", "serve", "--as", "dev"]},
		"web": {"command": "/usr/bin/serve", "args": ["--port", "8080"]}}}`)
	found, _ := Registrations(home, t.TempDir())
	if len(found) != 1 || found[0].Name != "dev" || found[0].As() != "dev" {
		t.Errorf("found %+v, want the one server that starts rta mcp serve", found)
	}
	if (Registration{Args: []string{"mcp", "serve"}}).As() != "" {
		t.Error("a registration with no --as reported a name")
	}
}
