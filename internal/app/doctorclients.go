package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	agentcap "github.com/this-is-tobi/rta/builtin/agent"
)

// The doctor's rows about AI clients: is any agent attached right now, and
// where is Claude Code told to start rta.
//
// Both exist because of one report — "I run several Claude Code sessions and
// never see any traffic" — that turned out to be three different situations
// the ledger cannot tell apart: a client attached and not calling, a client
// registered for one directory and started in another, and a server writing
// its record somewhere else. The first is answered by presence
// (internal/session); the second is answered here, by reading the one client
// config rta knows the shape of well enough to read without guessing.
//
// Claude Code keeps three places: the project's .mcp.json (shared, committed),
// and in ~/.claude.json a user-wide `mcpServers` and a per-project entry
// under `projects` (the default of `claude mcp add`, which is "this directory
// only"). rta reads and never writes these — the same rule mcpinstall.go
// states for every client's file.

// claudeRegistration is one place Claude Code was told about rta.
type claudeRegistration struct {
	as    string
	scope string // "every project", "this project (.mcp.json)", "this directory only"
}

func asFromArgs(args []any) string {
	for i, a := range args {
		if s, ok := a.(string); ok && s == "--as" && i+1 < len(args) {
			if v, ok := args[i+1].(string); ok {
				return v
			}
		}
	}
	return ""
}

func rtaEntry(servers any) (string, bool) {
	m, ok := servers.(map[string]any)
	if !ok {
		return "", false
	}
	for name, v := range m {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := entry["command"].(string)
		args, _ := entry["args"].([]any)
		if name == "rta" || strings.HasSuffix(cmd, "/rta") || cmd == "rta" || filepath.Base(cmd) == "rta.exe" {
			return asFromArgs(args), true
		}
	}
	return "", false
}

// claudeRegistrations reads where Claude Code starts rta from, for the
// working directory. home and dir are parameters so a test can point them
// at a fixture.
func claudeRegistrations(home, dir string) []claudeRegistration {
	var out []claudeRegistration
	if body, err := os.ReadFile(filepath.Join(dir, ".mcp.json")); err == nil {
		var doc struct {
			Servers any `json:"mcpServers"`
		}
		if json.Unmarshal(body, &doc) == nil {
			if as, ok := rtaEntry(doc.Servers); ok {
				out = append(out, claudeRegistration{as: as, scope: "this project (.mcp.json)"})
			}
		}
	}
	body, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return out
	}
	var doc struct {
		Servers  any `json:"mcpServers"`
		Projects map[string]struct {
			Servers any `json:"mcpServers"`
		} `json:"projects"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return out
	}
	if as, ok := rtaEntry(doc.Servers); ok {
		out = append(out, claudeRegistration{as: as, scope: "every project"})
	}
	if p, ok := doc.Projects[dir]; ok {
		if as, ok := rtaEntry(p.Servers); ok {
			out = append(out, claudeRegistration{as: as, scope: "this directory only"})
		}
	}
	return out
}

// clientRows is what the doctor says about agents: who is attached now, and
// how Claude Code is registered when it is.
func clientRows(claudeInstalled bool) [][3]string {
	var rows [][3]string
	connected, n := agentcap.Connected()
	if n == 0 {
		rows = append(rows, [3]string{"agents connected", "info", "none — no client has an rta server open right now; " +
			"a client that is registered but not running, or running in a directory it was not registered for, looks exactly like this"})
	} else {
		rows = append(rows, [3]string{"agents connected", "ok", connected + " (`rta agent overview`)"})
	}

	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	regs := claudeRegistrations(home, wd)
	if len(regs) == 0 {
		if claudeInstalled {
			rows = append(rows, [3]string{"claude code", "info", "rta is not registered for this directory — `rta mcp install claude`, " +
				"or `claude mcp add --scope user` to register it for every project"})
		}
		return rows
	}
	self, _ := os.Executable()
	for _, r := range regs {
		as := r.as
		if as == "" {
			as = "(no --as: calls are recorded under no name and grants cannot target it)"
		}
		detail := "starts rta as " + as + " — " + r.scope
		status := "ok"
		if r.scope == "this directory only" {
			status = "info"
			detail += "; a session opened elsewhere has no rta. For every project: `claude mcp add --scope user rta -- " +
				self + " mcp serve --as " + r.as + "`"
		}
		rows = append(rows, [3]string{"claude code", status, detail})
	}
	return rows
}
