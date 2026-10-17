package all

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A destructive capability names what it destroys, and a name nobody can list
// is a name typed from memory into the one call that cannot be taken back.
// Every required text input of one offers the names there are — a list it
// declares (Suggest) or a closed set (Options) — on the command line and in
// the TUI's form alike, so `net hosts rm`, `note rm` and `kv rm` complete the
// thing they remove, and a capability added later cannot be the one that does not.
//
// There is no list of exceptions: today every such input completes. One that
// names a value which does not exist yet (the new name of a rename) is not
// destructive on that input, and a capability with such an input beside a
// destructive one is the first reason to say so here.
func TestEveryDestructiveCapabilityCompletesTheNamesItActsOn(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range reg.Capabilities() {
		if c.Safety != plugin.Destructive {
			continue
		}
		for _, f := range c.Inputs {
			if !f.Required || (f.Type != plugin.String && f.Type != plugin.StringSlice) {
				continue
			}
			checked++
			if f.Suggest == nil && len(f.Options) == 0 {
				t.Errorf("%s: the required input %q names what the call destroys and offers no names "+
					"to complete (declare Suggest or Options)", c.ID, f.Name)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no input of a destructive capability was checked: the declarations changed shape under this test")
	}
}
