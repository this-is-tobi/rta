package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agentcap "github.com/this-is-tobi/rta/builtin/agent"
	agentsession "github.com/this-is-tobi/rta/internal/session"
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

// selfVersion is this process's build stamp, recorded when the command tree
// is built.
//
// Package state, for the reason profile.go's `installed` already documents:
// it is fixed once at startup, and the one reader is the doctor capability,
// which runs through the registry and is handed a registry and nothing else.
// Empty — in a test, or under a caller that never built a root — makes
// olderBuilds say nothing, which is the right zero for a row whose whole
// claim is "these two differ".
var selfVersion string

// olderBuilds names the open servers running something other than this
// build, or "" when every one of them is on it.
//
// **A process keeps the code it started with.** Claude Code holds an rta
// server open for days, so `rta upgrade` — or a `go install` during
// development — replaces the binary underneath servers that go on answering.
// Every surface a person reads is a fresh process on the new build, so the
// two disagree about the same files with nothing saying so. The shape that
// costs an afternoon: a grant issued from the config, listed healthy by
// `grant list` and by this page, and refused by an old server whose notion
// of the connection predates it — under the sentence a call with no grant at
// all gets, because the refusal deliberately tells an agent nothing about
// the operator's configuration (see refuseMissing). The remedy belongs on a
// person's screen, and this is the screen.
//
// The comparison is the build string and not a timestamp, so it is exact:
// no version arithmetic, no guessing from mtimes, and nothing to say on the
// ordinary case where every server is current. An empty version is the
// server that predates the field, which is older still.
func olderBuilds(version string) string {
	open, err := agentsession.List()
	if err != nil || version == "" {
		return ""
	}
	var named []string
	for _, s := range open {
		if s.Version == version {
			continue
		}
		was := s.Version
		if was == "" {
			was = "a build from before rta recorded it"
		}
		who := s.Agent
		if who == "" {
			who = "unnamed"
		}
		named = append(named, fmt.Sprintf("%s (pid %d, %s)", who, s.PID, was))
	}
	if len(named) == 0 {
		return ""
	}
	return fmt.Sprintf("%s running a build this is not (%s): %s — a server keeps the code it "+
		"started with, so one started before an upgrade decides by the old rules while this page, "+
		"`rta grant list` and the TUI read the new ones. A grant can be listed healthy here and "+
		"refused there, with the same words an ungranted call gets. Reconnect the client to pick "+
		"this build up",
		plural(len(named), "is on", "are on"), version, strings.Join(named, ", "))
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
func clientRows(claudeInstalled bool, version string) [][3]string {
	var rows [][3]string
	connected, n := agentcap.Connected()
	if n == 0 {
		rows = append(rows, [3]string{"agents connected", "info", "none — no client has an rta server open right now; " +
			"a client that is registered but not running, or running in a directory it was not registered for, looks exactly like this"})
	} else {
		rows = append(rows, [3]string{"agents connected", "ok", connected + " (`rta agent overview`)"})
		if older := olderBuilds(version); older != "" {
			rows = append(rows, [3]string{"agents connected", "warn", older})
		}
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
