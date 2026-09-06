package agentlog

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/seal"
)

// legacyEntry is Entry as the binary before the code/reason split marshalled
// it: no "code" key, the cause glued into reason as "code: message". The
// field order is the old struct's, which matters — see the test below.
type legacyEntry struct {
	Seq     int64          `json:"seq"`
	At      time.Time      `json:"at"`
	Cap     string         `json:"capability"`
	Tool    string         `json:"tool,omitempty"`
	Args    map[string]any `json:"args,omitempty"`
	Agent   string         `json:"agent,omitempty"`
	Client  string         `json:"client,omitempty"`
	Cred    string         `json:"credential,omitempty"`
	Profile string         `json:"profile,omitempty"`
	Outcome Outcome        `json:"outcome"`
	Auth    Authorization  `json:"auth"`
	Reason  string         `json:"reason,omitempty"`
	Millis  int64          `json:"ms,omitempty"`
	Missed  int64          `json:"missed,omitempty"`
	Prev    string         `json:"prev"`
	Seal    string         `json:"seal"`
}

// The seal is recomputed by re-marshalling the *parsed* entry (sealOf), so a
// pre-split line verifies only while marshal(parse(line)) reproduces the old
// binary's exact bytes: Code must stay omitempty, keep its JSON name, and
// hold its position — any of those drifting would make every pre-upgrade
// ledger read as tampered while the whole suite stayed green. This test is
// the upgrade scenario itself: a line sealed in the old shape, the new
// binary appending after it, the chain whole across the boundary.
func TestARecordWrittenBeforeTheCodeSplitStillVerifies(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	key, err := seal.Key(keyFile, true)
	if err != nil {
		t.Fatal(err)
	}
	old := legacyEntry{
		Seq:     1,
		At:      time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
		Cap:     "kv.get",
		Args:    map[string]any{"key": "db"},
		Agent:   "claude-desktop",
		Outcome: Refused,
		Auth:    Blocked,
		Reason:  "core.grant.required: no active grant",
		Millis:  3,
	}
	body, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	old.Seal = seal.MAC(key, append([]byte(old.Prev), body...))
	line, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	// The new binary appends after the old line — chaining to its seal and
	// writing the head — exactly what the first call after an upgrade does.
	if err := Append(Entry{Cap: "sys.cpu", Outcome: Ran, Auth: Open}); err != nil {
		t.Fatal(err)
	}

	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("a pre-split line no longer verifies — Code drifted out of "+
			"byte-compatibility (omitempty, name, or position): %+v", rep)
	}

	entries, err := Read(0)
	if err != nil || len(entries) != 2 {
		t.Fatalf("read: %v, %d entries", err, len(entries))
	}
	got := entries[0]
	if got.Code != "" || got.Reason != "core.grant.required: no active grant" {
		t.Fatalf("the old row must read back unchanged — glued reason, no code: %+v", got)
	}
	if entries[1].Prev != old.Seal {
		t.Fatalf("the new row does not chain to the old one: %q vs %q", entries[1].Prev, old.Seal)
	}
}

// The point of sealing the line rather than the struct: a record written by
// a binary that knows a field this one does not must still verify, and must
// not have that field quietly dropped from what was checked.
//
// The old scheme could not do this. It re-marshalled the parsed entry, so an
// unknown field vanished from the bytes being MACed and every such line read
// as tampered — which is the same failure an operator would see after
// downgrading, or after any future change to Entry.
func TestALineFromANewerRTAStillVerifies(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	key, err := seal.Key(keyFile, true)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly what a newer binary would write: this entry's fields, plus one
	// it invented, with the seal last and computed over all of it.
	body := []byte(`{"seq":1,"at":"2026-08-01T12:00:00Z","capability":"sys.cpu",` +
		`"outcome":"ran","auth":"open","prev":"","future":"a field this rta has never heard of"}`)
	mac := seal.MAC(key, body)
	quoted, err := json.Marshal(mac)
	if err != nil {
		t.Fatal(err)
	}
	line := append(body[:len(body)-1:len(body)-1], `,"seal":`...)
	line = append(line, quoted...)
	line = append(line, '}', '\n')
	if err := os.WriteFile(Path(), line, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Append(Entry{Cap: "sys.mem", Outcome: Ran, Auth: Open}); err != nil {
		t.Fatal(err)
	}
	rep, err := Verify()
	if err != nil {
		t.Fatal(err)
	}
	if rep.Broken != 0 {
		t.Fatalf("a line carrying an unknown field read as tampered: %+v", rep)
	}
	if rep.Entries != 2 {
		t.Fatalf("entries = %d, want both", rep.Entries)
	}
}
