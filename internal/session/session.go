// Package session is which MCP servers are open right now, and under what
// name.
//
// The agent ledger answers "what did an agent do"; it cannot answer "is an
// agent even attached", and that is the question behind every report of
// "I never see any traffic". A client that is connected and has not chosen
// an rta tool yet, a client that was registered for one directory and
// started in another, and a server writing its record to a different data
// directory than the one the TUI reads all look identical from the ledger:
// nothing. This package makes the first of those visible so the others can
// be told apart from it.
//
// One file per server process, written when a client completes the MCP
// handshake and removed when the server exits. It is presence, not audit —
// nothing decides anything on it, so it is a plain JSON file and not a link
// in the hash chain. A server that died without cleaning up leaves a file
// naming a pid that no longer exists; List drops those and removes them, so
// a stale file never reads as a live agent.
package session

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/internal/atomicfile"
	"github.com/this-is-tobi/rta/internal/paths"
)

// Record is one open server.
type Record struct {
	// ID is the short identifier this server stamps on every ledger entry
	// it writes, which is what lets `rta agent log --session` find the
	// calls of one client among several started under the same --as name.
	ID string `json:"id"`
	// Agent is the --as name, the operator's word; Client is what the client
	// announced about itself in the handshake — the same pair, with the same
	// trust in each, as the ledger keeps.
	Agent  string    `json:"agent,omitempty"`
	Client string    `json:"client,omitempty"`
	Since  time.Time `json:"since"`
	PID    int       `json:"pid"`
	// Dir is where the server was started, which for a client that
	// registers rta per project is the project.
	Dir string `json:"dir,omitempty"`
	// Ledger is the record this server writes to, so a mismatch with the
	// one the TUI reads is visible from the TUI's side.
	Ledger string `json:"ledger,omitempty"`
}

func Dir() string { return filepath.Join(paths.Data(), "sessions") }

func path(id string) string { return filepath.Join(Dir(), id+".json") }

// NewID is eight hex characters from the system's randomness: short enough
// to type after --session, and not derived from the pid, which the OS reuses.
func NewID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

// Start records this server as open.
func Start(r Record) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path(r.ID), body, 0o600)
}

// End removes the record. A missing file is not an error: End runs on every
// exit path and the file may never have been written.
func End(id string) error {
	if id == "" {
		return nil
	}
	err := os.Remove(path(id))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// List is every server open right now, oldest first. A record whose process
// is gone is removed on the way, so a crash never leaves a ghost agent on
// the dashboard.
func List() ([]Record, error) {
	entries, err := os.ReadDir(Dir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Record
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		p := filepath.Join(Dir(), e.Name())
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(body, &r) != nil || r.ID == "" {
			_ = os.Remove(p)
			continue
		}
		if !alive(r.PID) {
			_ = os.Remove(p)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out, nil
}
