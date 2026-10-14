package grant

import (
	"strings"
	"testing"
	"time"

	core "github.com/this-is-tobi/rta/internal/grant"
	profiles "github.com/this-is-tobi/rta/internal/profile"
	"github.com/this-is-tobi/rta/pkg/view"
)

func heldWarning(tbl view.Table) *view.Error {
	for i := range tbl.Warnings {
		if tbl.Warnings[i].Code == "grant.held" {
			return &tbl.Warnings[i]
		}
	}
	return nil
}

// `rta use staging` makes every grant that names another environment cover
// nothing, and each of them stays issued and listed exactly as it was: the
// roster, which is the screen somebody opens to learn what an agent may do
// right now, held the dead grants among the live ones with nothing to tell
// them apart.
func TestTheRosterSaysWhichGrantsTheSwitchedOnEnvironmentIsHolding(t *testing.T) {
	setup(t)
	now := time.Now()
	standing := func(g core.Grant) core.Grant {
		g.Agent, g.ProfilePin, g.Issued, g.Expires = "test", "pin", now, now.Add(time.Hour)
		return g
	}
	if verr := core.Save([]core.Grant{
		standing(core.Grant{Target: "pg", Profile: "prod"}),
		standing(core.Grant{Target: "pg", Profile: "staging"}),
		standing(core.Grant{Target: "pg", Profile: "staging/analytics"}),
		standing(core.Grant{Target: "kv.get", Scope: "db-password"}),
	}); verr != nil {
		t.Fatal(verr)
	}

	if w := heldWarning(listWith(t, builtIn, false)); w != nil {
		t.Fatalf("nothing is switched on and the roster says a grant is held: %+v", w)
	}

	if verr := profiles.SaveSelection(profiles.Selection{Active: "staging"}); verr != nil {
		t.Fatal(verr)
	}
	tbl := listWith(t, builtIn, false)
	w := heldWarning(tbl)
	if w == nil || !w.Advisory {
		t.Fatalf("the grant for prod is held and the roster does not say so: %+v", tbl.Warnings)
	}
	for _, want := range []string{"1 grant covers nothing while staging is switched on", "it names prod"} {
		if !strings.Contains(w.Message, want) {
			t.Errorf("the warning reads %q, want %q", w.Message, want)
		}
	}
	if !strings.Contains(w.Hint, "`rta use prod` switches to it") || !strings.Contains(w.Hint, "`rta use --off`") {
		t.Errorf("the hint reads %q, want the way back", w.Hint)
	}
	if len(tbl.Rows) != 4 {
		t.Errorf("the roster lists %d grants, want every one that stands", len(tbl.Rows))
	}
}

// A selection that cannot be believed holds every grant that names an
// environment, and the cause is the file, so that is what is said.
func TestAnUnverifiedSelectionIsNamedAsTheCauseOfHeldGrants(t *testing.T) {
	grants := []core.Grant{{Target: "pg", Profile: "prod"}, {Target: "kv.get"}}
	w := switchWarning(grants, profiles.Unverified)
	if w == nil || !strings.Contains(w.Message, "does not verify") || !strings.Contains(w.Hint, "rta doctor") {
		t.Fatalf("an unverifiable selection holds a grant and the roster says %+v", w)
	}
	if w := switchWarning([]core.Grant{{Target: "kv.get"}}, profiles.Unverified); w != nil {
		t.Errorf("a grant that names no environment is held by one that cannot be read: %+v", w)
	}
}
