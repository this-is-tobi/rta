package lockdown

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func fresh(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func mustAdd(t *testing.T, kind, name, note, ttl string) Lock {
	t.Helper()
	l, verr := Build(kind, name, note, ttl, "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	if verr := Add(l); verr != nil {
		t.Fatal(verr)
	}
	return l
}

func TestALockRoundTripsAndReplacesOnRelock(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "crashlooping", "")
	locks, verr := Load()
	if verr != nil || len(locks) != 1 || locks[0].Note != "crashlooping" {
		t.Fatalf("Load = %+v, %v", locks, verr)
	}
	// Re-locking replaces the row rather than stacking a second.
	mustAdd(t, "agent", "claude", "still crashlooping", "")
	locks, _ = Load()
	if len(locks) != 1 || locks[0].Note != "still crashlooping" {
		t.Fatalf("relock did not replace: %+v", locks)
	}
	removed, verr := Remove(KindAgent, "claude")
	if verr != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, verr)
	}
	if removed, _ := Remove(KindAgent, "claude"); removed {
		t.Fatal("a second remove claims something was there")
	}
	if locks, _ := Load(); len(locks) != 0 {
		t.Fatalf("locks survive removal: %+v", locks)
	}
}

func TestBuildRefusesWhatItShould(t *testing.T) {
	fresh(t)
	if _, verr := Build("agnet", "x", "", "", "terminal"); verr == nil || verr.Code != "core.lock.kind" {
		t.Fatalf("a typo'd kind built: %v", verr)
	}
	if _, verr := Build("agent", "x", "", "soon", "terminal"); verr == nil || verr.Code != "core.lock.ttl" {
		t.Fatalf("a garbled ttl built: %v", verr)
	}
	if _, verr := Build("agent", "x", "", "-5m", "terminal"); verr == nil || verr.Code != "core.lock.ttl" {
		t.Fatalf("a negative ttl built: %v", verr)
	}
	if verr := Add(Lock{Kind: KindAgent, Name: "bad\x1b[31mname"}); verr == nil {
		t.Fatal("a terminal-escape principal enrolled")
	}
}

func TestATTLdLockLiftsItself(t *testing.T) {
	fresh(t)
	l, verr := Build("credential", "alice", "", "1h", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	l.Expires = time.Now().Add(-time.Minute)
	if verr := Add(l); verr != nil {
		t.Fatal(verr)
	}
	if locks, _ := Load(); len(locks) != 0 {
		t.Fatalf("an expired lock is still listed: %+v", locks)
	}
	p := NewPin()
	if l, _ := p.Check("", "alice"); l != nil {
		t.Fatal("an expired lock still freezes")
	}
}

func TestThePinChecksBothMCPIdentities(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "", "")
	mustAdd(t, "credential", "alice", "", "")
	p := NewPin()
	if l, _ := p.Check("claude", ""); l == nil || l.Kind != KindAgent {
		t.Fatalf("the agent lock did not match: %+v", l)
	}
	if l, _ := p.Check("other", "alice"); l == nil || l.Kind != KindCredential {
		t.Fatalf("the credential lock did not match: %+v", l)
	}
	if l, _ := p.Check("other", "bob"); l != nil {
		t.Fatalf("an unlocked pair matched: %+v", l)
	}
}

// The failure direction is the guard's, not the grant store's: deleting
// grants.json removes authority, deleting lockdown.json would restore it.
// So a file that vanishes or stops verifying after this process saw locks
// changes nothing for the process, and says so once — while a legitimate
// Remove rewrites the sealed file and propagates on the next check.
func TestDeletingTheFileIsNotAnUnlock(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "", "")
	p := NewPin()
	if l, _ := p.Frozen(KindAgent, "claude"); l == nil {
		t.Fatal("the lock did not take")
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	l, alarm := p.Frozen(KindAgent, "claude")
	if l == nil {
		t.Fatal("rm'ing the file unlocked the agent for a process that saw the lock")
	}
	if alarm == "" {
		t.Fatal("the vanished file raised no alarm")
	}
	if _, again := p.Frozen(KindAgent, "claude"); again != "" {
		t.Fatal("the alarm repeats every call instead of once per incident")
	}
	// A fresh process is the documented detection limit: on-disk absence
	// wins across restarts.
	if l, _ := NewPin().Frozen(KindAgent, "claude"); l != nil {
		t.Fatal("a fresh process invented a lock from nothing")
	}
	// The legitimate direction: Remove rewrites the sealed file, and the
	// surviving pin honours it.
	mustAdd(t, "agent", "claude", "", "")
	if l, _ := p.Frozen(KindAgent, "claude"); l == nil {
		t.Fatal("the re-added lock did not take")
	}
	if _, verr := Remove(KindAgent, "claude"); verr != nil {
		t.Fatal(verr)
	}
	if l, _ := p.Frozen(KindAgent, "claude"); l != nil {
		t.Fatal("a legitimate unlock did not propagate to the running pin")
	}
}

func TestATamperedFileFailsClosedAndIsNotBuiltUpon(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "", "")
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(data), "claude", "cluade", 1)
	if forged == string(data) {
		t.Fatal("the tamper did not tamper")
	}
	if err := os.WriteFile(Path(), []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, verr := Load(); verr == nil || verr.Code != "core.lock.forged" {
		t.Fatalf("a tampered file loaded: %v", verr)
	}
	// Mutating over unverifiable bytes would launder them.
	if verr := Add(Lock{Kind: KindAgent, Name: "other", At: time.Now()}); verr == nil || verr.Code != "core.lock.forged" {
		t.Fatalf("Add built on a tampered file: %v", verr)
	}
	// A fresh process that never saw the good file: nothing to hold — the
	// corrupt bytes must not become locks — but the corruption is still an
	// alarm, because a rewritten trust file is worth a stderr line whoever
	// restarts on top of it.
	if l, alarm := NewPin().Frozen(KindAgent, "claude"); l != nil || alarm == "" {
		t.Fatalf("a fresh pin over a corrupt file: lock=%+v alarm=%q — want no lock, an alarm", l, alarm)
	}
}

func TestTheSealedShapeIsWhatWeThink(t *testing.T) {
	fresh(t)
	mustAdd(t, "operator", "dash", "", "")
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Seal  string          `json:"seal"`
		Locks json.RawMessage `json:"locks"`
	}
	if err := json.Unmarshal(data, &doc); err != nil || doc.Seal == "" || len(doc.Locks) == 0 {
		t.Fatalf("the on-disk shape drifted: %s (%v)", data, err)
	}
}
