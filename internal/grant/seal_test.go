package grant

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The attack this exists for, end to end. A writer that cannot read the data
// directory — which is exactly the shape a filesystem sandbox creates, and
// exactly what a confined plugin is — could author the answer to "what is
// this agent allowed to do", because loadAll was ReadFile plus Unmarshal and
// the Grant struct is public. No read needed: 82 bytes turned a refused
// kv.get into the secret.
func TestAForgedGrantFileIsRefused(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)

	// A legitimate grant first, so a seal key exists — the realistic state,
	// and the one where only a forged seal (not a missing key) can be the
	// reason for refusal.
	issue(t, Grant{Target: "todo.rm", Scope: "1"})
	if verr := gate(t, c, map[string]any{"key": "k"}, "", ""); verr == nil {
		t.Fatal("an ungranted call was authorized")
	}

	forged := `{"grants":[{"target":"kv","scope":"","issued":"2026-01-01T00:00:00Z","expires":"2099-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(Path(), []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	verr := gate(t, c, map[string]any{"key": "k"}, "", "")
	if verr == nil {
		t.Fatal("a forged grant file authorized the call")
	}
	if verr.Code != "core.grant.forged" {
		t.Errorf("refused with %q, want the forged code — the operator has to be able to tell "+
			"tampering from having issued nothing", verr.Code)
	}
}

// Copying a legitimate seal onto edited grants must fail too, or the seal
// covers the file's existence rather than its contents.
func TestASealCannotBeReusedForDifferentGrants(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "todo.rm", Scope: "1"})

	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Seal   string  `json:"seal"`
		Grants []Grant `json:"grants"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	// Same seal, wider grant.
	doc.Grants[0].Target = "kv"
	doc.Grants[0].Scope = ""
	out, _ := json.Marshal(doc)
	if err := os.WriteFile(Path(), out, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, verr := Load(); verr == nil || verr.Code != "core.grant.forged" {
		t.Fatalf("a replayed seal over edited grants was accepted: %v", verr)
	}
}

// MaxTTL was enforced where a grant is asked for and nowhere else, so the cap
// lived in the CLI and the file was trusted to have been written by it. A
// grant claiming to expire in 2099 was honoured for seventy years by a rule
// whose comment reads "a day is already generous".
func TestAGrantCannotOutliveMaxTTLHoweverItWasWritten(t *testing.T) {
	now := time.Now()
	g := Grant{
		Target:  "kv.get",
		Issued:  now.Add(-48 * time.Hour),
		Expires: now.Add(100 * 365 * 24 * time.Hour),
	}
	if g.Active(now) {
		t.Error("a grant issued two days ago with a 2126 expiry is still active")
	}
	// And the cap does not bind a grant issued normally.
	fresh := Grant{Target: "kv.get", Issued: now, Expires: now.Add(DefaultTTL)}
	if !fresh.Active(now) {
		t.Error("a freshly issued grant was refused by the cap")
	}
}

// A missing key with grants present is the state a forger leaves behind if
// they delete the key rather than forge a seal. It must not silently become
// "no grants": the operator needs to know the difference.
func TestAMissingSealKeyIsReportedNotIgnored(t *testing.T) {
	setup(t)
	issue(t, Grant{Target: "kv.get", Scope: "k"})
	if err := os.Remove(keyPath()); err != nil {
		t.Fatal(err)
	}
	_, verr := Load()
	if verr == nil {
		t.Fatal("grants loaded with no key to check them against")
	}
	if !strings.Contains(verr.Code, "unsealed") {
		t.Errorf("code = %q", verr.Code)
	}
}

// A grant file written before the seal existed. rta wrote this shape itself
// until v0.5, so it is on real disks — and the first version of the seal
// refused it with a JSON error naming an unexported type, which took down
// `grant list`, `rta doctor`, the dashboard tile and every gated MCP call at
// once.
func TestAPreSealGrantFileIsDroppedRatherThanRefused(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	// Exactly what Save wrote before sealed{} existed: a bare array, and one
	// grant inside it that has not expired.
	old := `[{"target":"kv","issued":"` + time.Now().Format(time.RFC3339Nano) +
		`","expires":"` + time.Now().Add(time.Hour).Format(time.RFC3339Nano) + `"}]`
	if err := os.WriteFile(Path(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}

	grants, verr := Load()
	if verr != nil {
		t.Fatalf("a pre-seal file was refused: %v", verr)
	}
	// The half that is about security: the grant inside it must not be
	// honoured, or an attacker skips the seal by writing the old shape.
	if len(grants) != 0 {
		t.Fatalf("a pre-seal grant was honoured: %+v", grants)
	}
	if verr := gate(t, declare("kv.get", plugin.Read, "", true), map[string]any{}, "", ""); verr == nil {
		t.Error("a pre-seal file authorized a gated call")
	}
	// And the half that is about the operator: nothing else may break.
	if _, verr := Load(); verr != nil {
		t.Errorf("a second read failed: %v", verr)
	}
	if !Legacy() {
		t.Error("Legacy() did not report the pre-seal file, so no surface can explain it")
	}
}

// Dropping the old file must not become a way to skip the seal on the new
// one: only a leading "[" is the legacy shape.
func TestOnlyABareArrayCountsAsPreSeal(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want bool
	}{
		{"legacy", `[{"target":"kv"}]`, true},
		{"legacy with leading space", "  \n\t[]", true},
		{"sealed", `{"seal":"ab","grants":[]}`, false},
		{"empty", ``, false},
		{"garbage", `not json`, false},
	} {
		if got := legacy([]byte(tc.data)); got != tc.want {
			t.Errorf("%s: legacy(%q) = %v, want %v", tc.name, tc.data, got, tc.want)
		}
	}
}

// A sealed file that is merely unparseable is still an error: the operator
// should hear about a grants.json full of garbage, and dropping it silently
// is only defensible for a shape rta itself is known to have written.
func TestACorruptSealedFileIsStillRefused(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	if err := os.WriteFile(Path(), []byte(`{"seal":`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, verr := Load()
	if verr == nil {
		t.Fatal("a corrupt grant file was accepted")
	}
	if verr.Code != "core.grant.corrupt" {
		t.Errorf("code = %q, want core.grant.corrupt", verr.Code)
	}
}

// A key file that is there but too short is a state to refuse, not to
// overwrite.
//
// Every grant on disk is sealed with the key that was in force when it was
// issued. Generating a fresh one over the top of a truncated file makes the
// *next* read report `core.grant.forged` — "written by something other than
// rta" — about a file rta wrote itself, and points the operator at deleting
// their grants to fix damage the recovery caused. Refusing names the actual
// problem and leaves the evidence where it is.
func TestASealKeyTooShortIsRefusedRatherThanReplaced(t *testing.T) {
	setup(t)
	if err := os.MkdirAll(filepath.Dir(keyPath()), 0o755); err != nil {
		t.Fatal(err)
	}
	stub := []byte("truncated")
	if err := os.WriteFile(keyPath(), stub, 0o600); err != nil {
		t.Fatal(err)
	}

	_, verr := sealKey(true)
	if verr == nil {
		t.Fatal("sealKey accepted a short key file")
	}
	if verr.Code != "core.grant.unsealed" {
		t.Errorf("code = %q, want core.grant.unsealed", verr.Code)
	}
	got, err := os.ReadFile(keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(stub) {
		t.Errorf("key file is now %q — the short one was replaced, invalidating every grant sealed with the original", got)
	}
}

// Everybody creating the key at once ends up with the same one.
//
// Writers are serialized by acquireLock today, but that is a fact about
// Save's callers rather than about sealKey, and the consequence of getting
// it wrong is silent: each process seals with its own key, the last write
// wins the file, and every grant sealed with a loser's key reads as forged.
func TestConcurrentCreatorsAgreeOnOneSealKey(t *testing.T) {
	setup(t)
	const n = 8
	keys := make([][]byte, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k, verr := sealKey(true)
			if verr != nil {
				t.Error(verr)
				return
			}
			keys[i] = k
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !bytes.Equal(keys[0], keys[i]) {
			t.Fatalf("creator 0 and creator %d ended up with different seal keys — grants sealed by one read as forged to the other", i)
		}
	}
	onDisk, err := os.ReadFile(keyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(onDisk, keys[0]) {
		t.Error("the key on disk is not the one the creators are using")
	}
}

// What the seal covers, in both directions of version skew — because the two
// are not the same and getting it wrong once already produced a false comment
// and a test that proved nothing.
//
// The MAC is taken over the *parsed* grants (canonical is json.Marshal of
// []Grant), so it covers the fields the writing build declared and is checked
// against the fields the reading build declares.
func TestTheSealAcrossVersionSkew(t *testing.T) {
	// Upgrading: a field a newer build added is absent from what an older one
	// wrote, so omitempty makes both sides encode identical bytes. This is the
	// direction the Profile and ProfilePin field comments claim, and it holds.
	t.Run("an older build's file still verifies", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("RTA_DATA_DIR", dir)
		now := time.Now()
		if verr := Save([]Grant{{
			Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(time.Hour),
		}}); verr != nil {
			t.Fatal(verr)
		}
		loaded, verr := Load()
		if verr != nil {
			t.Fatalf("a grant with no new field populated no longer verifies: %v", verr)
		}
		if len(loaded) != 1 {
			t.Fatalf("loaded %d grants, want the one that is there", len(loaded))
		}
	})

	// Downgrading: once a grant *populates* a field the reading build lacks,
	// the writer sealed over bytes that include it, the reader re-encodes
	// without it, and the MAC fails. It has to fail — the reader cannot check
	// what it cannot represent — but it must not be reported as forgery.
	//
	// Built the only way that actually exercises it: the seal is computed over
	// content that CARRIES the unknown field. An earlier version of this test
	// injected the field into a file this build had already sealed, which
	// proves a field added *after* sealing is uncovered — true, and not the
	// question.
	t.Run("a newer build's file is not called forged", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("RTA_DATA_DIR", dir)
		now := time.Now().UTC().Truncate(time.Second)

		// A future rta's grant: everything this build knows, plus a field it
		// does not.
		future := []map[string]any{{
			"target":           "kv.get",
			"scope":            "db-password",
			"issued":           now,
			"expires":          now.Add(time.Hour),
			"rateLimitPerHour": 12,
		}}
		canon, err := json.Marshal(future)
		if err != nil {
			t.Fatal(err)
		}
		key, verr := sealKey(true)
		if verr != nil {
			t.Fatal(verr)
		}
		body, err := json.MarshalIndent(map[string]any{
			"seal": sealOf(key, canon), "grants": future,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "grants.json"), body, 0o600); err != nil {
			t.Fatal(err)
		}

		_, verr = Load()
		if verr == nil {
			t.Fatal("a file this build cannot fully represent was honoured")
		}
		if verr.Code != "core.grant.unknownfields" {
			t.Fatalf("code = %q, want the newer-writer refusal rather than an accusation: %v",
				verr.Code, verr)
		}
		if strings.Contains(verr.Message, "something other than rta") {
			t.Errorf("still accuses the operator's own rta: %s", verr.Message)
		}
		if !strings.Contains(verr.Message, "rateLimitPerHour") {
			t.Errorf("does not name the field it could not read: %s", verr.Message)
		}
		if !strings.Contains(verr.Hint, "upgrade rta") {
			t.Errorf("hint = %q, want the remedy that keeps the grants", verr.Hint)
		}
	})

	// And tampering still reads as tampering. Every field this build declares
	// is inside the MAC, so editing one is caught with the accusation earned.
	t.Run("an edited grant is still called forged", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			edit func(map[string]json.RawMessage)
		}{
			{"a scope widened to another record", func(g map[string]json.RawMessage) {
				g["scope"] = json.RawMessage(`"prod-token"`)
			}},
			{"a scope removed, widening it to every record", func(g map[string]json.RawMessage) {
				delete(g, "scope")
			}},
			{"a deadline pushed out", func(g map[string]json.RawMessage) {
				g["expires"] = json.RawMessage(`"2099-01-01T00:00:00Z"`)
			}},
			{"a connection pin substituted", func(g map[string]json.RawMessage) {
				g["profile"] = json.RawMessage(`"staging"`)
				g["profilePin"] = json.RawMessage(`"forged"`)
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				t.Setenv("RTA_DATA_DIR", dir)
				now := time.Now()
				if verr := Save([]Grant{{
					Target: "kv.get", Scope: "db-password", Issued: now, Expires: now.Add(time.Hour),
				}}); verr != nil {
					t.Fatal(verr)
				}
				path := filepath.Join(dir, "grants.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var doc struct {
					Seal   string                       `json:"seal"`
					Grants []map[string]json.RawMessage `json:"grants"`
				}
				if err := json.Unmarshal(data, &doc); err != nil {
					t.Fatal(err)
				}
				tc.edit(doc.Grants[0])
				out, err := json.MarshalIndent(doc, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, out, 0o600); err != nil {
					t.Fatal(err)
				}
				loaded, verr := Load()
				if verr == nil {
					t.Fatalf("an edited grant was honoured: %+v", loaded)
				}
				if verr.Code != "core.grant.forged" {
					t.Errorf("code = %q, want the forgery refusal", verr.Code)
				}
			})
		}
	})
}

// The known-field set is read off the struct, never written out by hand.
//
// A hand-maintained list goes stale the day a field is added, which is the
// exact day it matters: the new field would be reported unknown by the very
// build that writes it, so every file rta produced would refuse itself.
func TestKnownFieldsTrackTheStruct(t *testing.T) {
	declared := declaredFields()
	for _, name := range []string{"target", "scope", "profile", "profilePin", "issued",
		"expires", "note", "ttl", "maxUses", "uses"} {
		if !declared[name] {
			t.Errorf("%q is a Grant field and is not recognised — a file this build writes "+
				"would refuse itself", name)
		}
	}
	now := time.Now()
	canon, err := canonical([]Grant{{
		Target: "pg", Profile: "staging", ProfilePin: "abc", TTL: "1h", MaxUses: 3, Uses: 1,
		Scope: "x", Note: "why", Issued: now, Expires: now.Add(time.Hour),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if extra := unknown([]byte(`{"seal":"x","grants":` + string(canon) + `}`)); len(extra) > 0 {
		t.Errorf("this build's own grants report unknown fields: %v", extra)
	}
}
