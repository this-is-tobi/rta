package kv

import (
	"strings"
	"testing"
)

// A form asks for the passphrase once, and for a store not made yet that one
// answer is the lock. The terminal's prompt repeats itself for that case; the
// TUI's box and a --passphrase flag cannot, so what the box says is the only
// warning a person typing into it gets, on every operation that shows it.
func TestThePassphraseBoxSaysANewStoreIsLockedWithWhatIsTyped(t *testing.T) {
	for _, want := range []string{"not made yet", "locked with it", "nothing recovers it"} {
		if !strings.Contains(passphraseField.Help, want) {
			t.Errorf("the passphrase help is %q, want it to say %q", passphraseField.Help, want)
		}
	}
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if f.Name == "passphrase" && f.Help != passphraseField.Help {
				t.Errorf("%s declares its own passphrase help %q", c.ID, f.Help)
			}
		}
	}
}
