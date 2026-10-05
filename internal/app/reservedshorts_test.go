package app

import (
	"slices"
	"testing"

	"github.com/spf13/pflag"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A one-letter flag is a name a capability command resolves before it looks
// above it, so a plugin input declaring the letter of a flag the host owns
// would silently take that flag over, as an input named after a long one does.
// plugin.reservedShorts is the host's declaration of which letters that
// applies to and the SDK cannot see cobra, so this test is what keeps the two
// in step, in both directions: a letter the CLI gained must be reserved, and a
// letter reserved for a flag no capability command has is a letter refused for
// nothing (-v is root's own and is the case that was once reserved wrongly).
func TestTheCLIReservesEveryOneLetterFlagItOwns(t *testing.T) {
	reserved := plugin.ReservedShorts()

	root := NewRoot(testRegistry(t), "test")
	reg := testRegistry(t)
	if len(reg.Capabilities()) == 0 {
		t.Fatal("the test registry has no capabilities, so this test checks nothing")
	}
	leaf, _, err := root.Find(reg.Capabilities()[0].Words())
	if err != nil || leaf == root {
		t.Fatalf("cannot reach the capability command: %v", err)
	}
	leaf.InitDefaultHelpFlag()

	held := map[string]bool{}
	leaf.Flags().VisitAll(func(f *pflag.Flag) {
		if f.Shorthand == "" {
			return
		}
		held[f.Shorthand] = true
		if !slices.Contains(reserved, f.Shorthand) {
			t.Errorf("%s can be given -%s (--%s) and nothing reserves that letter: a plugin input "+
				"declaring Short %q silently takes the flag over. Add it to plugin.reservedShorts.",
				leaf.CommandPath(), f.Shorthand, f.Name, f.Shorthand)
		}
	})
	for _, letter := range reserved {
		if !held[letter] {
			t.Errorf("%q is reserved but %s has no flag with that letter; either it is stale, or the "+
				"flag it protects was renamed", letter, leaf.CommandPath())
		}
	}
}
