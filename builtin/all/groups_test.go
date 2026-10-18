package all

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// A form draws an input under the group the author declared and, for a connection
// input that declares none, the one its name and type fall back to (plugin.Group).
// The fallback is right for nearly every input in the catalogue, so a built-in
// declares a group only where it would be wrong or silent: a declaration that says
// what the fallback already says is one more line to keep true.
func TestABuiltinDeclaresAGroupOnlyWhereTheFallbackIsWrongOrSilent(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			if f.Group == "" || !plugin.Connection(c, f) {
				continue
			}
			bare := f
			bare.Group = ""
			if fallback := plugin.Group(c, bare); fallback == f.Group {
				t.Errorf("%s declares %s into %q, which is where the fallback puts it: drop the declaration",
					c.ID, f.Name, f.Group)
			}
		}
	}
}

// What is declared, so that a change to the fallback that moves one of these is
// seen: a resolver is where a query goes, not what is selected inside a service,
// and the ways to authenticate are one block where no config states a connection
// for the fallback to find them in.
func TestTheBuiltinsGroupWhatTheFallbackWouldMissOrGetWrong(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]map[string]string{}
	for _, c := range reg.Capabilities() {
		for _, f := range c.Inputs {
			if g := plugin.Group(c, f); g != "" {
				if groups[c.ID] == nil {
					groups[c.ID] = map[string]string{}
				}
				groups[c.ID][f.Name] = g
			}
		}
	}
	for id, want := range map[string]map[string]string{
		"net.dns":  {"server": "network"},
		"http.get": {"bearer": "credentials", "basic": "credentials", "bearer-file": "credentials", "basic-file": "credentials"},
		"kv.get":   {"passphrase": "credentials", "identity": "credentials"},
		"kv.set":   {"passphrase": "credentials", "identity": "credentials"},
		// A connection that no declaration touches: the fallback reads its name.
		"audit.kube.rbac": {"context": "network"},
	} {
		for input, group := range want {
			if got := groups[id][input]; got != group {
				t.Errorf("%s --%s is in %q, want %q", id, input, got, group)
			}
		}
	}
	// The call's own inputs are in no block: what is being asked about is not the
	// connection, however it is spelled.
	for id, inputs := range map[string][]string{"http.get": {"url", "header", "timeout"}, "net.dns": {"name", "type"}} {
		for _, input := range inputs {
			if g := groups[id][input]; g != "" {
				t.Errorf("%s --%s is in %q, want no group", id, input, g)
			}
		}
	}
}
