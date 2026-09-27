package grant

import (
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

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
