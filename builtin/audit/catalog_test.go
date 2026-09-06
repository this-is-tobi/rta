package audit

import "github.com/this-is-tobi/rta/pkg/plugin"

// testCatalog stands in for the registry: two plugins whose verbs are all
// for the person at the terminal, and one that mixes.
func testCatalog() []plugin.Capability {
	return []plugin.Capability{
		{ID: "grant.allow", HumanOnly: true}, {ID: "grant.list", HumanOnly: true},
		{ID: "agent.allow", HumanOnly: true},
		{ID: "kv.get"}, {ID: "kv.copy", HumanOnly: true},
	}
}
