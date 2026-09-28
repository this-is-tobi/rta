package grant

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A record that is only white space is one no grant can name: grant allow
// refuses it, and so does every other way a grant is issued. The refusal
// handed on the command `grant allow kv.get ' ' --ttl 15m` all the same,
// which only refuses in turn; it hands on no command for one, and says why.
func TestTheHintOffersNoGrantOnARecordOfWhiteSpace(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	refused := func(record string) *view.Error {
		t.Helper()
		_, verr := Reserve(c, map[string]any{"key": record}, Caller{Agent: "claude"})
		if verr == nil || verr.Code != "core.grant.required" {
			t.Fatalf("%q: went through ungranted: %v", record, verr)
		}
		return verr
	}
	for _, blank := range []string{" ", "\t", string(rune(0xa0))} {
		if verr := refused(blank); strings.Contains(verr.Hint, "grant allow") || !strings.Contains(verr.Hint, "only white space") {
			t.Errorf("%q: hint = %q, want no command and the reason there is none", blank, verr.Hint)
		}
	}
	if verr := refused(" db"); !strings.Contains(verr.Hint, "grant allow kv.get ' db' --agent claude --ttl 15m") {
		t.Errorf("a padded record's hint = %q, want the command granting it as spelled", verr.Hint)
	}
}

// Every way a grant is issued refuses a record of white space alone, the
// ones that never read a record a person typed included: a --ttl answer
// issues the record the agent's call named, and the operator channel's
// issue verb the one a submitted grant carries. Such a grant could be named
// by no revoke or renew, which take a record as given and refuse this one.
func TestNoGrantIsIssuedOnARecordOfWhiteSpace(t *testing.T) {
	setup(t)
	for _, blank := range []string{" ", "\t\n", string(rune(0xa0))} {
		verr := Issue(Grant{Target: "kv.get", Scope: blank, Agent: "claude",
			Issued: time.Now(), Expires: time.Now().Add(time.Hour)}, true)
		if verr == nil || verr.Code != "grant.scope.blank" {
			t.Errorf("%q: issued with %v, want grant.scope.blank", blank, verr)
		}
	}
	if grants, _ := Load(); len(grants) != 0 {
		t.Fatalf("a grant on white space was stored: %+v", grants)
	}
	for _, record := range []string{"", " db", "db" + string(rune(0xa0))} {
		if verr := CheckScope(record); verr != nil {
			t.Errorf("%q: refused with %v, a record that names something", record, verr)
		}
	}
}

// The grant command a refusal hands on names the record the way the refusal
// shows it. A record ending in a character that draws as nothing is shown
// quoted, its character named, and the command went on printing it in plain
// single quotes, where it read as the bare record quoted: the command a person
// runs from the hint has to say which record it grants, not the one it looks
// like. Each is spelled by its bytes instead.
func TestTheHintsGrantCommandSpellsACharacterThatDrawsAsNothing(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	for _, r := range []rune{0x3164, 0x2800, 0xfe0f} {
		padded := "db-password" + string(r)
		verr := gate(t, c, map[string]any{"key": padded}, "", "")
		if verr == nil || verr.Code != "core.grant.required" {
			t.Fatalf("U+%04X: a padded record went through: %v", r, verr)
		}
		if strings.ContainsRune(verr.Hint, r) || !strings.Contains(verr.Hint, "grant allow kv.get $'db-password\\") {
			t.Errorf("U+%04X: hint = %q, want the record spelled by its bytes", r, verr.Hint)
		}
	}
}
