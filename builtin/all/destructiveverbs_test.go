package all

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// removals are the last words of a capability whose act is to take something
// away: rta's own rm, and the words the systems it speaks to use for that act.
var removals = map[string]bool{
	"rm": true, "delete": true, "destroy": true, "purge": true,
	"drop": true, "remove": true, "revoke": true, "wipe": true,
}

// An agent's client learns whether a tool destroys from its class and from
// nothing else: MCP's destructiveHint is Safety == Destructive. http.delete was
// a write beside http.post and http.put, classed by its neighbours rather than
// by its act, and every client was told that a DELETE only ever adds. A
// capability an agent can reach whose verb is a removal is destructive, so
// the next one cannot be classed by its neighbours either.
//
// A HumanOnly one is never a tool and is left out, which is also what keeps
// the rule off the two it would get wrong: grant.revoke and lock.rm take
// authority away rather than anything of the person's.
func TestEveryRemovalAnAgentCanReachIsDestructive(t *testing.T) {
	reg, err := Registry(nil)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, c := range reg.Capabilities() {
		words := c.Words()
		if !removals[words[len(words)-1]] || c.HumanOnly {
			continue
		}
		checked++
		if c.Safety != plugin.Destructive {
			t.Errorf("%s is %s, which tells an agent's client it only adds: a removal is destructive",
				c.ID, c.Safety)
		}
	}
	if checked == 0 {
		t.Fatal("no removal was checked: the catalogue changed shape under this test")
	}
}
