package app

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	agentcap "github.com/this-is-tobi/rta/builtin/agent"
	"github.com/this-is-tobi/rta/builtin/audit"
	"github.com/this-is-tobi/rta/internal/atomicfile"
	agentsession "github.com/this-is-tobi/rta/internal/session"
	"github.com/this-is-tobi/rta/pkg/format"
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

// claudeScope is where Claude Code keeps one registration, named as its own
// `--scope` flag names it.
type claudeScope string

const (
	scopeLocal   claudeScope = "local"
	scopeUser    claudeScope = "user"
	scopeProject claudeScope = "project"
)

// where is the scope as the operator reads it.
func (s claudeScope) where() string {
	switch s {
	case scopeUser:
		return "every project"
	case scopeProject:
		return "this project (.mcp.json)"
	}
	return "this directory only"
}

// claudeRegistration is one place Claude Code was told about rta: the scope it
// sits in and the entry itself, whole, so that a second install can tell the
// registration it would make from the one that is there.
type claudeRegistration struct {
	as      string
	scope   claudeScope
	name    string
	command string
	args    []string
	// env is the names of the variables the entry sets, and never their
	// values: rta registers none, so any that are there are the operator's,
	// and a value is as likely to be a credential as a data directory.
	env []string
}

// managed says whether the entry is the one `rta mcp install` makes and may
// therefore replace or take out: registered under the name rta gives it, and
// starting rta's server. An entry that launches rta under another name is the
// operator's own and is only ever reported, and so is a server that is not
// rta's at all in the name rta would have used: it is theirs, and taking it out
// to put rta there would delete what rta did not write. The client refuses the
// add, as it always did, and the answer says which line takes it out.
func (r claudeRegistration) managed() bool { return r.name == "rta" && servesMCP(r.args) }

func asFromArgs(args []string) string {
	for i, a := range args {
		if a == "--as" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// servesMCP says whether args are rta's own `mcp serve`, whatever the binary
// that precedes them is called.
func servesMCP(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "mcp" && args[i+1] == "serve" {
			return true
		}
	}
	return false
}

func stringsOf(raw []any) []string {
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// envNames is the sorted names of the variables an entry's env object sets.
func envNames(raw any) []string {
	set, _ := raw.(map[string]any)
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// rtaEntry finds the declaration in a `mcpServers` object that launches rta:
// the one named "rta" when there is one, else the first by name. By name, not
// by map order: two declarations that both launch rta used to be told apart by
// whichever the iteration reached first, and a report that changes between two
// runs on one file is not one to act on.
func rtaEntry(servers any) (claudeRegistration, bool) {
	m, ok := servers.(map[string]any)
	if !ok {
		return claudeRegistration{}, false
	}
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	var other *claudeRegistration
	for _, name := range names {
		entry, ok := m[name].(map[string]any)
		if !ok {
			continue
		}
		cmd, _ := entry["command"].(string)
		raw, _ := entry["args"].([]any)
		args := stringsOf(raw)
		if name != "rta" && !strings.HasSuffix(cmd, "/rta") && cmd != "rta" && !servesMCP(args) {
			continue
		}
		found := claudeRegistration{as: asFromArgs(args), name: name, command: cmd, args: args,
			env: envNames(entry["env"])}
		if name == "rta" {
			return found, true
		}
		if other == nil {
			other = &found
		}
	}
	if other != nil {
		return *other, true
	}
	return claudeRegistration{}, false
}

// olderBuilds names the open servers running something other than this
// build, or "" when every one of them is on it.
//
// The list is session.OtherBuilds; what is here is the wording, because this
// is the screen an operator opens when a grant they can see is refused
// anyway. The refusal itself will not tell them — it tells an agent nothing
// about the operator's configuration, deliberately (see grant.refuseMissing)
// — so the remedy has to be findable here.
func olderBuilds(version string) string {
	others := agentsession.OtherBuilds(version)
	if len(others) == 0 {
		return ""
	}
	named := make([]string, 0, len(others))
	for _, s := range others {
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
	return fmt.Sprintf("%s running a build this is not (%s): %s — a server keeps the code it "+
		"started with, so one started before an upgrade decides by the old rules while this page, "+
		"`rta grant list` and the TUI read the new ones. A grant can be listed healthy here and "+
		"refused there, with the same words an ungranted call gets. Reconnect the client to pick "+
		"this build up",
		format.Count(len(named), "server is", "servers are"), version, strings.Join(named, ", "))
}

// claudeRegistrations reads where Claude Code starts rta from, for the
// working directory. home and dir are parameters so a test can point them
// at a fixture.
//
// The directory entry is looked up under the path as given and, when that
// differs, under the same path with its links resolved: Claude Code keys a
// directory by the path it was started in, and a shell's $PWD that goes
// through a link (/tmp on macOS) is a different string for the same place.
func claudeRegistrations(home, dir string) []claudeRegistration {
	var out []claudeRegistration
	add := func(scope claudeScope, servers any) {
		if r, ok := rtaEntry(servers); ok {
			r.scope = scope
			out = append(out, r)
		}
	}
	if body, err := atomicfile.ReadFile(filepath.Join(dir, ".mcp.json")); err == nil {
		var doc struct {
			Servers any `json:"mcpServers"`
		}
		if json.Unmarshal(body, &doc) == nil {
			add(scopeProject, doc.Servers)
		}
	}
	body, err := atomicfile.ReadFile(filepath.Join(home, ".claude.json"))
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
	add(scopeUser, doc.Servers)
	keys := []string{dir}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		keys = append(keys, resolved)
	}
	for _, key := range keys {
		if p, ok := doc.Projects[key]; ok {
			add(scopeLocal, p.Servers)
			break
		}
	}
	return out
}

// clientRows is what the doctor says about agents: who is attached now, and
// how Claude Code is registered when it is.
func clientRows(claudeInstalled bool, version string, detail bool) [][3]string {
	var rows [][3]string
	connected, n, err := agentcap.Connected()
	switch {
	case err != nil:
		rows = append(rows, [3]string{"agents connected", "warn",
			"could not check: " + err.Error()})
	case n == 0:
		// Ok, and not a note: a client that is not running is the ordinary state
		// of a machine, and nothing here is asked of the operator. What it is
		// worth knowing is that a registered client that is not connected looks
		// exactly like this, which is the first thing to rule out when no
		// traffic shows up.
		rows = append(rows, [3]string{"agents connected", "ok", said(detail,
			"none — no client has an rta server open right now",
			"; a client that is registered but not running, or running in a directory it was not "+
				"registered for, looks exactly like this")})
	default:
		rows = append(rows, [3]string{"agents connected", "ok", connected + " (`rta agent overview`)"})
		if older := olderBuilds(version); older != "" {
			rows = append(rows, [3]string{"agents connected", "warn", older})
		}
	}

	home, _ := os.UserHomeDir()
	wd, _ := os.Getwd()
	return append(rows, claudeRows(claudeInstalled, claudeRegistrations(home, wd))...)
}

// claudeRows says where Claude Code starts rta from, and what a session opened
// somewhere else would find. The remedy it names is the command that makes the
// registration, not claude's own spelling of it: that is the one rta verified.
func claudeRows(claudeInstalled bool, regs []claudeRegistration) [][3]string {
	if len(regs) == 0 {
		if !claudeInstalled {
			return nil
		}
		return [][3]string{{"claude code", "info", "rta is not registered — `rta mcp install claude --global` " +
			"registers it for every project (without --global, for this directory only)"}}
	}
	everywhere := ""
	haveEverywhere := false
	for _, r := range regs {
		if r.scope == scopeUser {
			everywhere, haveEverywhere = r.as, true
		}
	}
	var rows [][3]string
	for _, r := range regs {
		status, detail := "ok", "starts rta as "+r.as+" — "+r.scope.where()
		if r.as == "" {
			// rta mcp serve refuses to start without a name, so this is a
			// registration that never connects.
			status, detail = "warn", "starts rta with no --as — "+r.scope.where()+
				", and the server refuses to start without one; `rta mcp install claude` registers it again"
			rows = append(rows, [3]string{"claude code", status, detail})
			continue
		}
		if r.scope == scopeLocal {
			switch {
			case !haveEverywhere:
				status = "info"
				detail += "; a session opened elsewhere has no rta. For every project: `rta mcp install claude --global`"
			case everywhere != r.as:
				status = "info"
				detail += fmt.Sprintf("; it overrides the every-project registration here, which starts it as %s, "+
					"so a grant issued to one does not reach the other", everywhere)
			}
		}
		rows = append(rows, [3]string{"claude code", status, detail})
	}
	return rows
}

// clientSeen is a client rta registers with, and how it is known to be on this
// machine: its own command on PATH, or the directory it leaves in the home
// directory once it has been run.
type clientSeen struct {
	client mcpClient
	cli    bool
	dir    bool
}

func (s clientSeen) present() bool { return s.cli || s.dir }

// seenClients is every client rta knows, with what could be told of it here.
func seenClients(home string) []clientSeen {
	clients := mcpClients()
	out := make([]clientSeen, 0, len(clients))
	for _, c := range clients {
		seen := clientSeen{client: c}
		if c.bin != "" {
			_, err := exec.LookPath(c.bin)
			seen.cli = err == nil
		}
		if c.dir != nil && home != "" {
			info, err := os.Stat(c.dir(home))
			seen.dir = err == nil && info.IsDir()
		}
		out = append(out, seen)
	}
	return out
}

// otherClientRows says, for every client but Claude Code that is on this
// machine, whether rta is registered with it. Read-only: the files are opened
// by the reader `rta audit clients` uses, and a client that is not here earns
// no row, since "not registered" about a tool nobody installed is noise.
func otherClientRows(home, wd string) [][3]string {
	found, unread := audit.Registrations(home, wd)
	var rows [][3]string
	for _, seen := range seenClients(home) {
		c := seen.client
		if c.name == "claude" || !seen.present() {
			continue
		}
		registered := false
		for _, r := range found {
			if !strings.HasPrefix(r.Label, c.auditLabel) {
				continue
			}
			registered = true
			if r.As() == "" {
				rows = append(rows, [3]string{c.name, "warn", "starts rta with no --as in " + shortHome(r.File, home) +
					", and the server refuses to start without one; `rta mcp install " + c.name + "` registers it again"})
				continue
			}
			rows = append(rows, [3]string{c.name, "ok", "starts rta as " + r.As() + " — " + shortHome(r.File, home)})
		}
		if registered {
			continue
		}
		var cannot []string
		for _, u := range unread {
			if strings.HasPrefix(u.Label, c.auditLabel) {
				cannot = append(cannot, shortHome(u.File, home))
			}
		}
		if len(cannot) > 0 {
			rows = append(rows, [3]string{c.name, "warn", "could not read " + strings.Join(cannot, ", ") +
				", so whether rta is registered with " + c.label + " is not known"})
			continue
		}
		rows = append(rows, [3]string{c.name, "info", c.label + " is here and rta is not registered with it — `rta mcp install " + c.name + "`"})
	}
	return rows
}

// shortHome spells a path under the home directory with a tilde.
func shortHome(path, home string) string {
	if home != "" && strings.HasPrefix(path, home+string(filepath.Separator)) {
		return "~" + path[len(home):]
	}
	return path
}
