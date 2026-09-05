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
