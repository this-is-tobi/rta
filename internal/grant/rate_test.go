package grant

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
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
	// The grant is named as the tool the agent called, like every other
	// sentence it is told, and not by the dotted ID of the capability.
	if !strings.HasPrefix(verr.Message, "the grant for kv_get allows 2 calls per 1h") {
		t.Errorf("the pace is not said in the tool's name: %q", verr.Message)
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

// A grant that covers one scope and has merely run out of pace must not
// masquerade as the reason a call is refused when a DIFFERENT scope in the
// same call has no covering grant at all — waiting out the rate limit would
// never fix that one, so telling the caller to wait is the wrong answer.
func TestARateLimitOnOneScopeIsNotBlamedForAnUnrelatedMissingOne(t *testing.T) {
	setup(t)
	c := plugin.Capability{
		ID: "kv.env", Summary: "kv.env", Safety: plugin.Write, Scope: "key", NeedsGrant: true,
		Inputs: []plugin.Field{{Name: "key", Type: plugin.StringSlice}},
		Run:    func(context.Context, plugin.Request) (view.View, error) { return view.Text{}, nil },
	}
	issue(t, paced("kv.env", "secretA", 1, "1h"))
	// No grant at all for secretB.

	if verr := call(t, c, map[string]any{"key": []string{"secretA"}}, "", ""); verr != nil {
		t.Fatalf("spending secretA's one use: %v", verr)
	}

	// secretA is now rate-exhausted; secretB was never covered by anything.
	verr := call(t, c, map[string]any{"key": []string{"secretA", "secretB"}}, "", "")
	if verr == nil {
		t.Fatal("expected a refusal")
	}
	if verr.Code == "core.grant.rate" {
		t.Fatalf("blamed the rate limit for a call that also names an uncovered scope "+
			"(secretB) — waiting cannot fix that one: %v", verr)
	}
	if verr.Code != "core.grant.required" {
		t.Fatalf("code = %q, want core.grant.required", verr.Code)
	}
	if !strings.Contains(verr.Message, "secretB") {
		t.Fatalf("refusal does not name the actually-uncovered scope: %v", verr)
	}
}

// atPace is n grants each at the most --rate allows in a day, with every one
// of those uses still inside its window: the most pace history the file can be
// asked to carry for them.
func atPace(n int) []Grant {
	now := time.Now()
	recent := make([]time.Time, MaxRate)
	for i := range recent {
		recent[i] = now.Add(-time.Duration(i) * time.Minute).UTC().Truncate(time.Second)
	}
	grants := make([]Grant, n)
	for i := range grants {
		grants[i] = paced("note.add", fmt.Sprintf("record-%d", i), MaxRate, "24h")
		grants[i].Issued, grants[i].Expires = now, now.Add(time.Hour)
		grants[i].Uses, grants[i].Recent = MaxRate, recent
	}
	return grants
}

// The pace history is stored without indentation. Each use inside a window
// is a timestamp in the file, and indented four levels deep each cost 32
// bytes, so the file filled at about 8,000 of them across every rate-limited
// grant — nine grants at a thousand a day — and from then on every counted
// call was refused, the call itself being what could not be written down.
// The seal is over the grants and not the bytes, so how the file is laid out
// is free to change.
func TestNineGrantsAtTheirFullPaceStillFitTheFile(t *testing.T) {
	setup(t)
	if verr := Save(atPace(9)); verr != nil {
		t.Fatalf("nine grants at %d calls a day: %v", MaxRate, verr)
	}
	stored, verr := loadAll()
	if verr != nil || len(stored) != 9 || len(stored[0].Recent) != MaxRate {
		t.Fatalf("read back %d grants, %v, want nine with their history", len(stored), verr)
	}
}

// When it is the pace history that fills the file, the refusal says so. It
// said that grants stand and to revoke some, to an operator holding a few
// grants whose calls were what had grown it: what helps is a rate with fewer
// calls or a shorter window, and time, as the calls leave their windows.
func TestAFileFilledByPaceHistoryIsRefusedForThat(t *testing.T) {
	setup(t)
	verr := Save(atPace(20))
	if verr == nil || verr.Code != "core.grant.full" {
		t.Fatalf("Save = %v, want core.grant.full", verr)
	}
	if !strings.Contains(verr.Message, "timestamps") || !strings.Contains(verr.Message, "20 rate-limited grants") {
		t.Errorf("message = %q, want the pace history named as what fills the file", verr.Message)
	}
	if !strings.Contains(verr.Hint, "--rate") || !strings.Contains(verr.Hint, "24h") {
		t.Errorf("hint = %q, want a smaller rate and the longest window named", verr.Hint)
	}
}

// Uses that have left their window are not written back, whichever grant the
// write was for. Only a call on the grant itself forgot them, so a grant
// nobody called again kept its last window's uses until it expired: counted
// by nothing, and filling the file for every other grant's counted calls,
// while the refusal said the history empties as calls leave their windows and
// waiting out the longest changed nothing.
func TestUsesOutsideTheirWindowAreNotWrittenBack(t *testing.T) {
	setup(t)
	idle := atPace(20)
	for i := range idle {
		idle[i].RateWindow = "1h"
		stale := make([]time.Time, len(idle[i].Recent))
		for j := range stale {
			stale[j] = idle[i].Recent[j].Add(-2 * time.Hour)
		}
		idle[i].Recent = stale
	}
	busy := paced("note.add", "busy", 5, "1h")
	busy.Issued, busy.Expires = time.Now(), time.Now().Add(time.Hour)
	inside := time.Now().Add(-10 * time.Minute).UTC().Truncate(time.Second)
	busy.Uses, busy.Recent = 2, []time.Time{time.Now().Add(-90 * time.Minute).UTC().Truncate(time.Second), inside}

	if verr := Save(append(idle, busy)); verr != nil {
		t.Fatalf("twenty idle grants whose uses all left the window: %v", verr)
	}
	stored, verr := loadAll()
	if verr != nil || len(stored) != 21 {
		t.Fatalf("read back %d grants, %v, want all 21", len(stored), verr)
	}
	for _, g := range stored[:20] {
		if g.Recent != nil {
			t.Fatalf("%s kept %d uses outside its window", g.Scope, len(g.Recent))
		}
	}
	if got := stored[20].Recent; len(got) != 1 || !got[0].Equal(inside) {
		t.Errorf("busy kept %v, want only the use inside its window", got)
	}
}
