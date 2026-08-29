package grant

import (
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
)

// A budget on how *fast* a grant may be spent, not just how much of it.
// MaxUses answers "how much of this may happen at all"; a leaked session can
// spend that in a second, which buys the operator nothing. The point of a
// pace is that a session which has gone wrong degrades into something slow
// enough to notice.

func paced(target, scope string, max int, window string) Grant {
	return Grant{Target: target, Scope: scope, RateMax: max, RateWindow: window}
}

func TestAPaceIsSpentAndThenRefuses(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	issue(t, paced("kv.get", "db", 2, "1h"))

	for i := 1; i <= 2; i++ {
		if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr != nil {
			t.Fatalf("call %d of 2 was refused: %v", i, verr)
		}
	}
	verr := call(t, c, map[string]any{"key": "db"}, "", "")
	if verr == nil {
		t.Fatal("a third call went through a two-per-hour pace")
	}
	// Its own code, so nothing downstream mistakes a full budget for a
	// missing grant — including consent, which must not ask about this.
	if verr.Code != "core.grant.rate" {
		t.Fatalf("code = %q, want core.grant.rate", verr.Code)
	}
	if !strings.Contains(verr.Hint, "try again in") {
		t.Fatalf("the refusal does not say when to come back: %q", verr.Hint)
	}
	// The grant is still there and still active: this is a pace, not an end.
	grants, gerr := Load()
	if gerr != nil || len(grants) != 1 {
		t.Fatalf("the grant vanished: %v %+v", gerr, grants)
	}
}

func TestThePaceRefillsAsTheWindowMovesOn(t *testing.T) {
	// A rolling window, so what refills is the oldest use rather than the
	// whole allowance at a boundary — a tumbling window would let twice the
	// limit through across one.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	g := paced("kv.get", "db", 2, "1h")
	// Two uses: one long past the window, one inside it.
	g.Recent = []time.Time{
		time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second),
		time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second),
	}
	g.Uses = 2
	issue(t, g)

	if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr != nil {
		t.Fatalf("the expired use did not refill the window: %v", verr)
	}
	// And now both slots inside the window are taken.
	if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr == nil {
		t.Fatal("a third call inside the window went through")
	}
}

func TestAFailedCallGivesBackThePaceAsWellAsTheUse(t *testing.T) {
	// A call that failed did not spend the operator's minute any more than
	// it spent their use, and leaving the timestamp behind would make a
	// flaky capability look like a busy agent.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	issue(t, paced("kv.get", "db", 1, "1h"))

	release, verr := Reserve(c, map[string]any{"key": "db"}, Caller{})
	if verr != nil {
		t.Fatal(verr)
	}
	release() // the call failed

	if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr != nil {
		t.Fatalf("the refund did not give the pace back: %v", verr)
	}
	grants, _ := Load()
	if len(grants) != 1 || len(grants[0].Recent) != 1 {
		t.Fatalf("recent = %v, want exactly the one use that stuck", grants[0].Recent)
	}
}

func TestRecentStaysBoundedByTheLimitItself(t *testing.T) {
	// The storage a rolling window costs is proportional to the limit the
	// operator chose, not to how long the grant has existed — anything else
	// grows a file nothing prunes.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	g := paced("kv.get", "db", 3, "1s")
	issue(t, g)
	for i := 0; i < 12; i++ {
		_ = call(t, c, map[string]any{"key": "db"}, "", "")
		time.Sleep(120 * time.Millisecond)
	}
	grants, _ := Load()
	if n := len(grants[0].Recent); n > 3 {
		t.Fatalf("recent holds %d timestamps for a 3-per-second pace", n)
	}
}

func TestAPaceThatWillNotParseAuthorizesNothing(t *testing.T) {
	// A hand-edited file. Corrupting one string must not turn a throttled
	// grant into an unthrottled one.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	issue(t, paced("kv.get", "db", 5, "every so often"))

	verr := call(t, c, map[string]any{"key": "db"}, "", "")
	if verr == nil {
		t.Fatal("a grant with an unreadable window authorized a call")
	}
	if verr.Code != "core.grant.rate" {
		t.Fatalf("code = %q", verr.Code)
	}
}

func TestAnAbsurdPaceAuthorizesNothing(t *testing.T) {
	// The same fail-closed reading for a limit past what rta will pace: the
	// CLI refuses to write one, so a grant carrying it was not written by
	// the CLI.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	issue(t, paced("kv.get", "db", MaxRate+1, "1h"))
	if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr == nil {
		t.Fatal("a grant claiming a pace beyond MaxRate authorized a call")
	}
}

func TestBothBudgetsBiteAndTheTighterOneWins(t *testing.T) {
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	g := paced("kv.get", "db", 10, "1h")
	g.MaxUses = 2
	issue(t, g)

	for i := 1; i <= 2; i++ {
		if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr != nil {
			t.Fatalf("call %d was refused: %v", i, verr)
		}
	}
	verr := call(t, c, map[string]any{"key": "db"}, "", "")
	if verr == nil {
		t.Fatal("the use limit did not bite under a looser pace")
	}
	// Spent, not paced: the tighter budget is the one the operator hears
	// about, and telling them to wait an hour for a grant that is finished
	// would send them to the wrong remedy.
	if verr.Code != "core.grant.required" {
		t.Fatalf("code = %q, want the spent-grant refusal", verr.Code)
	}
}

func TestAPaceCoversOneCallNamingSeveralRecords(t *testing.T) {
	// allocate walks every record a call names, so a pace has to be spent
	// per record like a use is — otherwise `kv env a b c` is one call and
	// three records against a one-per-hour pace.
	setup(t)
	c := declare("kv.env", plugin.Write, "key", true)
	issue(t, paced("kv.env", "", 2, "1h"))

	verr := call(t, c, map[string]any{"key": []string{"a", "b", "c"}}, "", "")
	if verr == nil {
		t.Fatal("three records went through a two-per-hour pace in one call")
	}
	// And nothing was spent for a call that was refused whole.
	grants, _ := Load()
	if len(grants[0].Recent) != 0 {
		t.Fatalf("a refused call spent %d of the pace", len(grants[0].Recent))
	}
}

func TestAGrantWithNoPaceIsUntouchedByAnyOfThis(t *testing.T) {
	// The common case has to stay exactly what it was: no timestamps, no
	// lock, no change to the file.
	setup(t)
	c := declare("kv.get", plugin.Write, "key", true)
	issue(t, Grant{Target: "kv.get", Scope: "db"})
	for i := 0; i < 5; i++ {
		if verr := call(t, c, map[string]any{"key": "db"}, "", ""); verr != nil {
			t.Fatalf("call %d: %v", i, verr)
		}
	}
	grants, _ := Load()
	if len(grants[0].Recent) != 0 || grants[0].Uses != 0 {
		t.Fatalf("an unbudgeted grant grew state: %+v", grants[0])
	}
}
