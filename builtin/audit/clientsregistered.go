package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
)

// Where rta is already registered, read from the files `audit clients` reads.
//
// **A second question over the same reader**, and not a second reader. `rta
// doctor` and `rta mcp install` need to know whether an MCP client has been
// told about rta, and the audit already knows where each client keeps that and
// how to walk it: a declaration is found by its shape (an object with a
// `command`), wherever it sits, so a client whose file moves its servers one
// level deeper is still read. A copy of that walk in the command layer would
// be wrong the week after a release, in a different way.
//
// It reads and never writes, for the reason the audit does.

// Registration is one declaration in a client's configuration that launches
// `rta mcp serve`, and where it was found.
type Registration struct {
	// Label names the client and, for a file under the working directory,
	// says so: "Cursor", "Cursor (this project)".
	Label string
	// File is the configuration it was found in.
	File string
	// Name is the key it is declared under, which is not always "rta".
	Name    string
	Command string
	Args    []string
}

// As is the agent name the declaration starts the server as, or "" for one
// that names none — a server that records its calls under no name, and that
// no grant can be issued to.
func (r Registration) As() string {
	for i, a := range r.Args {
		if a == "--as" && i+1 < len(r.Args) {
			return r.Args[i+1]
		}
	}
	return ""
}

// launchesRta says whether a declaration starts an rta MCP server. The two
// words after the binary are the test, because the binary's own name is
// whatever the operator's package, build or container made it; `mcp serve` is
// what the server is called wherever it lives.
func launchesRta(d serverDecl) bool {
	for i := 0; i+1 < len(d.args); i++ {
		if d.args[i] == "mcp" && d.args[i+1] == "serve" {
			return true
		}
	}
	return d.name == "rta" || filepath.Base(d.command) == "rta"
}

// Unread is a file that exists and could not be asked: one that might hold a
// registration and cannot say is not the same answer as one that holds none.
type Unread struct {
	Label, File string
}

// Registrations lists every place the clients rta knows keep their MCP
// servers declare an rta server, for this home directory and this working
// directory, and the files that could not be read.
func Registrations(home, wd string) (found []Registration, unread []Unread) {
	for _, f := range agentFiles(home, wd) {
		data, err := pathin.ReadFile(f.path, maxAgentConfigBytes)
		if err != nil {
			if !os.IsNotExist(err) {
				unread = append(unread, Unread{Label: f.label, File: f.path})
			}
			continue
		}
		doc, ok := configTree(f.path, data)
		if !ok {
			unread = append(unread, Unread{Label: f.label, File: f.path})
			continue
		}
		var servers []serverDecl
		collectServers(doc, &servers)
		for _, s := range servers {
			if s.command != "" && launchesRta(s) {
				found = append(found, Registration{Label: f.label, File: f.path,
					Name: s.name, Command: s.command, Args: s.args})
			}
		}
	}
	return found, unread
}

// configTree reads one client file into the tree collectServers walks:
// TOML for Codex, JSON for the rest, and JSON with comments and trailing
// commas when plain JSON will not parse — VS Code's mcp.json
// is the one an operator edits by hand and annotates.
func configTree(path string, data []byte) (any, bool) {
	if strings.EqualFold(filepath.Ext(path), ".toml") {
		doc, err := tomlTree(string(data))
		return doc, err == nil
	}
	var doc any
	if json.Unmarshal(data, &doc) == nil {
		return doc, true
	}
	if json.Unmarshal(stripJSONC(data), &doc) == nil {
		return doc, true
	}
	return nil, false
}
