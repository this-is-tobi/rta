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
// handshake, kept fresh while the server runs, and removed when it exits. It
// is presence, not audit — nothing decides anything on it, so it is a plain
// JSON file and not a link in the hash chain.
//
// # Liveness is a heartbeat, not a pid
//
// A server that dies without cleaning up leaves its file behind, and List
// has to tell that from a server that is simply quiet. Asking the OS whether
// the recorded pid still exists answers instantly and answers wrong often
// enough to matter: pids are reused, so a recycled number makes a dead
// session read as live for as long as the file sits there — which on the one
// screen an operator checks to answer "is anything even attached" is the
// failure that costs the most. It also took two platform-specific files to
// ask, one of which could only really answer "does a handle open".
//
// So an open server touches its own file every Beat, and List disbelieves
// anything older than stale. The cost is bounded and stated: a crashed
// server can linger for up to stale before it disappears. The benefit is
// that a live server is one that said so recently, which is the actual
// question, on every platform, in one file.
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
	// PID is recorded for a person reading the file or hunting a process.
	// Nothing decides on it — see the package comment on why liveness is a
	// heartbeat instead.
	PID int `json:"pid"`
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

// Beat is how often an open server refreshes its record, and stale is how
// long List keeps believing one that has not. Three beats of slack, so an
// ordinary scheduling hiccup on a loaded machine never evicts a live server.
const (
	Beat  = 30 * time.Second
	stale = 3 * Beat
)

// Touch refreshes this server's record so List keeps counting it. A record
// that is gone is not recreated: End removed it, or an operator cleared the
// directory, and either way this server is on its way out.
func Touch(id string) error {
	if id == "" {
		return nil
	}
	now := time.Now()
	err := os.Chtimes(path(id), now, now)
	if os.IsNotExist(err) {
		return nil
	}
	return err
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

// List is every server open right now, oldest first. A record nobody has
// touched within stale is removed on the way, so a crash never leaves a
// ghost agent on the dashboard.
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
		// The heartbeat is the file's own modification time, so keeping a
		// record fresh costs one Chtimes rather than a rewrite of the JSON
		// every half minute — and a reader that cannot stat it is a reader
		// that cannot read it either.
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) > stale {
			_ = os.Remove(p)
			continue
		}
		body, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var r Record
		if json.Unmarshal(body, &r) != nil || r.ID == "" {
			_ = os.Remove(p)
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Since.Before(out[j].Since) })
	return out, nil
}
