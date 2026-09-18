package lockdown

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/seal"
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

// The credential kind must be able to name what the verifiers actually
// prove — user@corp.com, auth0|12345, the truncated ~hash form long
// subjects arrive as — none of which the agent-name charset admits. The
// review that caught this named the stakes: during exactly the incident
// locks exist for, the operator could not lock the bearer.
func TestACredentialLockAcceptsWhatTheVerifiersProve(t *testing.T) {
	fresh(t)
	for _, name := range []string{"user@corp.com", "auth0|12345", "https://idp.example/sub/9",
		strings.Repeat("x", 55) + "~ab12cd34"} {
		if _, verr := Build("credential", name, "", "", "terminal"); verr != nil {
			t.Fatalf("a real credential identity %q refused: %v", name, verr)
		}
		// The same spelling stays refused for the kinds whose surfaces
		// enforce the agent-name grammar.
		if _, verr := Build("agent", name, "", "", "terminal"); verr == nil {
			t.Fatalf("%q enrolled as an agent name", name)
		}
	}
	if verr := Add(Lock{Kind: KindCredential, Name: strings.Repeat("x", maxCredentialName+1), At: time.Now()}); verr == nil {
		t.Fatal("a credential name past the ledger bound enrolled — it could never match")
	}
	if verr := Add(Lock{Kind: KindCredential, Name: "esc\x1b[31mape", At: time.Now()}); verr == nil {
		t.Fatal("a terminal-escape credential enrolled")
	}
}

// A note is a sentence for the locked party; unbounded it could write a
// file larger than the read cap and brick the store on its own input.
func TestANoteIsASentenceNotADocument(t *testing.T) {
	fresh(t)
	if _, verr := Build("agent", "claude", strings.Repeat("n", maxNote+1), "", "terminal"); verr == nil || verr.Code != "core.lock.note" {
		t.Fatalf("an oversized note built: %v", verr)
	}
	if _, verr := Build("agent", "claude", strings.Repeat("n", maxNote), "", "terminal"); verr != nil {
		t.Fatalf("a note at the bound refused: %v", verr)
	}
}

// A truncated lockdown.key makes every `rta lock add` fail forever —
// seal.Key regenerates nothing over bytes that are already there — and the
// refusal used to say lockdown.json exists with no usable key beside it,
// with a hint to rm that file and re-place the locks. Following it exactly
// looped: the file it meant may not exist at all on the write path, and the
// key it never named is the one thing that needs removing. Measured end to
// end before this was fixed: `rm lockdown.key` was the only recovery.
func TestATruncatedSealKeyNamesTheFileThatFixesIt(t *testing.T) {
	fresh(t)
	key := seal.Path(keyFile)
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("short"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, verr := Build("agent", "claude", "incident", "", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	verr = Add(l)
	if verr == nil || verr.Code != "core.lock.unsealed" {
		t.Fatalf("Add over a truncated key: %v, want core.lock.unsealed", verr)
	}
	if !strings.Contains(verr.Message, key) || strings.Contains(verr.Message, Path()) {
		t.Errorf("the message names the wrong file: %q — the key is the broken one, and lockdown.json is not there", verr.Message)
	}
	if !strings.Contains(verr.Hint, "rm -f "+key) {
		t.Errorf("the hint does not name the file that fixes it, in a form that cannot error on the one that is absent: %q", verr.Hint)
	}
	// The hint's recovery, followed: the key goes, and the next add works.
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	if verr := Add(l); verr != nil {
		t.Fatalf("Add after removing the truncated key: %v", verr)
	}
}

// The one trace deletion leaves: save writes the seal key before the file it
// authenticates and rta never removes it, so a key with no lockdown.json
// beside it is a sealed file that existed and is gone — or a save that
// failed after writing the key, or a key truncated with nothing sealed yet.
// Every one of those is worth a line in doctor, and none of them is a clean
// machine, which is what the absence of the file used to read as.
func TestAKeyLeftWithoutItsFileIsTheTraceDeletionLeaves(t *testing.T) {
	fresh(t)
	if KeyWithoutFile() {
		t.Fatal("a clean machine reads as one that lost its lock file")
	}
	mustAdd(t, "agent", "claude", "", "")
	if KeyWithoutFile() {
		t.Fatal("a machine with its lock file in place reads as one that lost it")
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	if !KeyWithoutFile() {
		t.Fatal("the key left behind by a removed lock file went unnoticed")
	}
	mustAdd(t, "agent", "claude", "", "")
	if KeyWithoutFile() {
		t.Fatal("re-placing a lock did not clear the signal")
	}
}

// mutate used to save whatever f returned, so `rta lock rm nosuchagent` on a
// clean machine minted both lockdown.json and lockdown.key for nothing —
// and a seal key minted for nothing is one more way into the truncation
// window. grant.Mutate already declines a write that changes nothing, with
// its reasoning written out; two shapes for one decision is what this
// repository avoids.
func TestRemovingNothingWritesNothing(t *testing.T) {
	fresh(t)
	removed, verr := Remove(KindAgent, "nobody")
	if verr != nil || removed {
		t.Fatalf("Remove of nothing = %v, %v", removed, verr)
	}
	for _, f := range []string{Path(), keyPath()} {
		if _, err := os.Stat(f); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists after a remove that matched nothing", f)
		}
	}
}

// No test ever called Remove with a lock that should survive: every case
// stored exactly one, so the branch that keeps the other rows had no
// coverage, and a Remove that dropped every row — or matched on name
// alone — would have passed the suite while `rta lock rm claude` unfroze
// every other principal mid-incident. The same-name-different-kind row is
// the one a name-only match would eat.
func TestRemoveLiftsOnlyTheNamedLock(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "crashlooping", "")
	mustAdd(t, "agent", "codex", "flooding", "")
	mustAdd(t, "credential", "claude", "leaked", "")
	removed, verr := Remove(KindAgent, "claude")
	if verr != nil || !removed {
		t.Fatalf("Remove = %v, %v", removed, verr)
	}
	locks, _ := Load()
	if len(locks) != 2 {
		t.Fatalf("locks after removing one of three: %+v", locks)
	}
	notes := map[string]string{}
	for _, l := range locks {
		notes[string(l.Kind)+" "+l.Name] = l.Note
	}
	if notes["agent codex"] != "flooding" || notes["credential claude"] != "leaked" {
		t.Errorf("the other locks did not survive intact: %v", notes)
	}
}

// Two ways a file stops being rta's: bytes that do not parse, and bytes that
// parse but do not carry the seal. TestATamperedFileFailsClosedAndIsNotBuiltUpon
// covers the second; this is the first, which is its own branch.
func TestAnUnparseableLockFileIsRefusedLikeAForgedOne(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "", "")
	if err := os.WriteFile(Path(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, verr := Load(); verr == nil || verr.Code != "core.lock.forged" {
		t.Fatalf("an unparseable file loaded: %v", verr)
	}
	if verr := Add(Lock{Kind: KindAgent, Name: "other", At: time.Now()}); verr == nil || verr.Code != "core.lock.forged" {
		t.Fatalf("Add built on an unparseable file: %v", verr)
	}
}

// A file that cannot be read is not an unlock either: the pin keeps the set
// it last verified, the way it does for a file that vanished.
func TestAnUnreadableLockFileIsNotAnUnlock(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	fresh(t)
	mustAdd(t, "agent", "claude", "", "")
	p := NewPin()
	if l, _ := p.Frozen(KindAgent, "claude"); l == nil {
		t.Fatal("the lock did not take")
	}
	if err := os.Chmod(Path(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(Path(), 0o600) })
	if _, verr := Load(); verr == nil || verr.Code != "core.lock.read" {
		t.Fatalf("an unreadable file: %v, want core.lock.read", verr)
	}
	l, alarm := p.Frozen(KindAgent, "claude")
	if l == nil || alarm == "" {
		t.Fatalf("an unreadable file unlocked the agent for a process that saw the lock (lock=%+v alarm=%q)", l, alarm)
	}
	if _, again := p.Frozen(KindAgent, "claude"); again != "" {
		t.Fatal("the alarm repeats every call instead of once per incident")
	}
}

// grant.CheckAgent accepts an empty name, so Add's own check is the only
// wall against a row that reads as protection and can match nothing —
// match() treats "" as no principal. `rta lock add ""` reaches it: the
// positional validator counts arguments and never checks emptiness.
func TestALockWithNoPrincipalIsRefused(t *testing.T) {
	fresh(t)
	for _, name := range []string{"", "\t "} {
		if verr := Add(Lock{Kind: KindAgent, Name: name, At: time.Now()}); verr == nil || verr.Code != "core.lock.name" {
			t.Errorf("Add with name %q: %v, want core.lock.name", name, verr)
		}
	}
	if locks, _ := Load(); len(locks) != 0 {
		t.Errorf("an empty-named row was stored: %+v", locks)
	}
}

// Build checks the kind, and so does Add, for a Lock handed to it directly
// — the shape the operator channel's handler uses.
func TestAddRefusesAKindItWasHandedDirectly(t *testing.T) {
	fresh(t)
	if verr := Add(Lock{Kind: Kind("root"), Name: "x", At: time.Now()}); verr == nil || verr.Code != "core.lock.kind" {
		t.Fatalf("Add with an unknown kind: %v, want core.lock.kind", verr)
	}
}

// load() drops expired rows on every read, but a pin holding the last
// verified set — because the file vanished — never reads again, so match()
// has to re-check expiry on the set it holds. Only that second check keeps
// a TTL'd lock in a held set from outliving its window.
func TestAnExpiredLockInAHeldSetIsNotALock(t *testing.T) {
	fresh(t)
	l, verr := Build("agent", "claude", "", "1h", "terminal")
	if verr != nil {
		t.Fatal(verr)
	}
	l.Expires = time.Now().Add(80 * time.Millisecond)
	if verr := Add(l); verr != nil {
		t.Fatal(verr)
	}
	p := NewPin()
	if held, _ := p.Frozen(KindAgent, "claude"); held == nil {
		t.Fatal("the lock did not take")
	}
	if err := os.Remove(Path()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if held, _ := p.Frozen(KindAgent, "claude"); held != nil {
		t.Fatal("an expired lock in the held set still freezes")
	}
}

// Adds are serialised by the file lock, which serialises goroutines as well
// as processes because the sentinel is a create-once file. Twelve at once
// all land; a lost write here is the class grant.Mutate's comment describes.
func TestConcurrentAddsAllLand(t *testing.T) {
	fresh(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, verr := Build("agent", fmt.Sprintf("agent-%d", i), "", "", "terminal")
			if verr != nil {
				errs <- verr
				return
			}
			if verr := Add(l); verr != nil {
				errs <- verr
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if locks, _ := Load(); len(locks) != 12 {
		t.Fatalf("%d of 12 concurrent adds landed", len(locks))
	}
}

// canonical re-marshals the parsed rows, so the seal binds what the rows
// say rather than the bytes they were written in: a re-indented file with
// an unrelated top-level key still verifies, and only a change inside a
// row is a forgery (TestATamperedFileFailsClosedAndIsNotBuiltUpon). A
// "simplification" to MAC the raw file bytes would start rejecting every
// re-indented file as forged, which is why the property is pinned.
func TestTheSealBindsTheRowsAndNotTheBytes(t *testing.T) {
	fresh(t)
	mustAdd(t, "agent", "claude", "incident", "")
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["written-by"] = "a newer rta"
	reshaped, err := json.MarshalIndent(doc, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), reshaped, 0o600); err != nil {
		t.Fatal(err)
	}
	locks, verr := Load()
	if verr != nil || len(locks) != 1 || locks[0].Note != "incident" {
		t.Fatalf("a re-indented file with an extra key: %+v, %v", locks, verr)
	}
}
