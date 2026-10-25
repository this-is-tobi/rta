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
// The inputs that are not such a name are listed below with the reason, and a
// listed one that stops being a required text input of a destructive
// capability fails here, so the list cannot outlive what it excuses. One that
// names a value which does not exist yet (the new name of a rename) is not
// destructive on that input.
var notNames = map[string]string{
	// The URL names a resource on another machine, which rta has no listing
	// of and no business asking for one: what this person sent before is
	// offered back (internal/recent), as it is for every input.
	"http.delete/url": "a resource on another machine",
	// The bytes net.send writes, which name nothing.
	"net.send/data": "the bytes it sends",
}

func TestEveryDestructiveCapabilityCompletesTheNamesItActsOn(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	checked, waived := 0, map[string]bool{}
	for _, c := range reg.Capabilities() {
		if c.Safety != plugin.Destructive {
			continue
		}
		for _, f := range c.Inputs {
			if !f.Required || (f.Type != plugin.String && f.Type != plugin.StringSlice) {
				continue
			}
			if _, listed := notNames[c.ID+"/"+f.Name]; listed {
				waived[c.ID+"/"+f.Name] = true
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
	for key, why := range notNames {
		if !waived[key] {
			t.Errorf("%s is listed as not a name (%s), and is no required text input of a destructive capability: "+
				"drop it from the list", key, why)
		}
	}
}
