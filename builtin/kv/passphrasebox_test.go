package kv

import (
	"strings"
	"testing"
)

// A form asks for the passphrase once, and for a store not made yet that one
// answer is the lock. The terminal's prompt repeats itself for that case; the
// TUI's box and a --passphrase flag cannot, so what kv.set's box says is the
// only warning a person typing into it gets. The other operations open a store
// that exists, and are not told about one that does not.
func TestOnlyTheBoxOfTheOperationThatMakesTheStoreSaysItIsLockedWithWhatIsTyped(t *testing.T) {
	for _, c := range Plugin().Capabilities {
		for _, f := range c.Inputs {
			if f.Name != "passphrase" {
				continue
			}
			says := strings.Contains(f.Help, "not made yet") &&
				strings.Contains(f.Help, "locked with it") && strings.Contains(f.Help, "nothing recovers it")
			if makes := c.ID == "kv.set"; says != makes {
				t.Errorf("%s: passphrase help %q, says it of a new store: %v, want %v", c.ID, f.Help, says, makes)
			}
		}
	}
}

func TestAnEmptyFirstAnswerForANewPassphraseIsNotAskedAgain(t *testing.T) {
	orig := readSecret
	t.Cleanup(func() { readSecret = orig })
	var asked []string
	readSecret = func(prompt string) ([]byte, error) {
		asked = append(asked, prompt)
		return nil, nil
	}
	first, again, err := promptNewPassphrase()
	if err != nil || first != "" || again != "" {
		t.Fatalf("got %q, %q, %v", first, again, err)
	}
	if len(asked) != 1 {
		t.Errorf("asked %q, want the first prompt only: an empty answer is no choice to confirm", asked)
	}
}
