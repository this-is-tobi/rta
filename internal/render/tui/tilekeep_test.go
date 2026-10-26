package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/internal/render/theme"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// pacedRegistry is one capability, db.status, whose runs are scripted and whose
// pace is the one declared (0 leaves it to the host).
func pacedRegistry(t *testing.T, script *scriptedRuns, refresh time.Duration) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
		ID: "db.status", Summary: "status", Safety: plugin.Read, Refresh: refresh,
		Run: func(context.Context, plugin.Request) (view.View, error) { return script.next() },
	}}}); err != nil {
		t.Fatal(err)
	}
	return reg
}

// answered is a dashboard of the one tile whose first run has been delivered.
func answered(t *testing.T, script *scriptedRuns, refresh time.Duration) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	m := New(pacedRegistry(t, script, refresh), config.Dashboard{Tiles: []config.Tile{{ID: "db.status"}}}, nil)
	return filled(t, m, 80, 24)
}

// launched is the same dashboard as it is the moment it opens: sized, with no
// run delivered, so what New decided about the first run is what is asked.
func launched(t *testing.T, script *scriptedRuns, refresh time.Duration) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	m := New(pacedRegistry(t, script, refresh), config.Dashboard{Tiles: []config.Tile{{ID: "db.status"}}}, nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return next.(Model)
}

// drawn is tile 1 as the dashboard paints it.
func drawn(m Model, width int) string {
	return renderTile(m.asReturned(1), width, 8, false, false)
}

func rawOf(m Model) returned { return m.tileRaw[rawKey(m.tiles[1])] }

// ageAnswer makes the tile's latest answer the age given, as time passing would.
func ageAnswer(m Model, age time.Duration) {
	r := rawOf(m)
	r.at = time.Now().Add(-age)
	m.tileRaw[rawKey(m.tiles[1])] = r
}

// stamp is how a time reads on a tile: the clock, with the day when it is not
// today's.
func stamp(at time.Time) string {
	if y, m, d := time.Now().Date(); at.Year() != y || at.Month() != m || at.Day() != d {
		return at.Format("Jan 2 15:04")
	}
	return at.Format("15:04")
}

// **A failed refresh threw the last good answer away without saying when it was
// good.** Seventy-five seconds after a backend died every tile was an error
// badge, and a slow run kept its old rows unchanged for as long as it took. The
// tile keeps what it last knew, dimmed, with when it knew it; a failure with
// nothing before it is the error, as it always was.
func TestAFailedRefreshDrawsTheLastGoodAnswerDimmedAndSaysWhenItWas(t *testing.T) {
	m := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	if m.tiles[1].err == nil {
		t.Fatal("the fixture's second run did not fail")
	}
	good := rawOf(m).goodAt
	if good.IsZero() {
		t.Fatal("the failure threw the last good answer away")
	}

	panel := drawn(m, 60)
	text := plain(panel)
	if want := "as of " + stamp(good) + " · refresh failed"; !strings.Contains(text, want) {
		t.Errorf("the panel does not say %q:\n%s", want, text)
	}
	if !strings.Contains(text, "up") || strings.Contains(text, "db.conn.timeout") {
		t.Errorf("the panel is not the last good answer:\n%s", text)
	}
	if got, want := fgBefore(t, panel, "up"), colourOf(t, theme.Subtle); got != want {
		t.Errorf("the old answer is drawn in %s, want it dimmed to %s", got, want)
	}
	assertExact(t, panel, 60, 8)
}

func TestAFailureWithNothingBeforeItIsDrawnAsItWas(t *testing.T) {
	m := answered(t, &scriptedRuns{errs: []*view.Error{timedOut()}}, 0)
	text := plain(drawn(m, 60))
	if !strings.Contains(text, "db.conn.timeout") || strings.Contains(text, "as of") {
		t.Errorf("a tile that never answered is not its error:\n%s", text)
	}
}

// The failure is still a failure for everything that reads the tile's state: the
// folded section's header counts it, and the border says so. Only the body is
// the old answer.
func TestAKeptAnswerStillGradesBadWhileItsRefreshIsFailing(t *testing.T) {
	m := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	if g, _ := tileGrade(m.asReturned(1)); g != gradeBad {
		t.Errorf("a tile whose refresh failed grades %v, want bad", g)
	}
	if got, want := borderOf(t, drawn(m, 60)), colourOf(t, theme.BadText); got != want {
		t.Errorf("the border is %s, want the bad colour %s", got, want)
	}
}

// The next good answer replaces the kept one, and the tile is drawn as itself.
func TestTheNextGoodAnswerReplacesTheKeptOne(t *testing.T) {
	m := askedAgain(t, askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut(), nil}}, 0)))
	if text := plain(drawn(m, 60)); strings.Contains(text, "as of") {
		t.Errorf("a tile that answered again still says when it was last good:\n%s", text)
	}
	if r := rawOf(m); r.err != nil || !r.goodAt.Equal(r.at) {
		t.Errorf("the kept answer is not the latest: %+v", r)
	}
}

// Twice its pace is the line between a tile that is slow and one that is old: a
// run that takes longer than its interval has not made the answer old yet.
func TestAnAnswerOlderThanTwiceItsPaceIsDrawnDimmedWithItsTime(t *testing.T) {
	m := answered(t, &scriptedRuns{errs: []*view.Error{nil}}, time.Minute)
	for age, old := range map[time.Duration]bool{
		30 * time.Second: false, 119 * time.Second: false, 125 * time.Second: true, 3 * time.Hour: true,
	} {
		ageAnswer(m, age)
		panel := drawn(m, 60)
		text := plain(panel)
		want := "as of " + stamp(rawOf(m).at)
		if got := strings.Contains(text, want); got != old {
			t.Errorf("an answer %s old says %q: %v, want %v:\n%s", age, want, got, old, text)
		}
		if old && strings.Contains(text, "refresh failed") {
			t.Errorf("an old answer that did not fail says it did:\n%s", text)
		}
		if old && fgBefore(t, panel, "up") != colourOf(t, theme.Subtle) {
			t.Errorf("an answer %s old is not dimmed", age)
		}
	}
}

// A tile on the host's own five seconds is older than twice that in ten.
func TestTheHostsOwnPaceIsHeldToTheSameRule(t *testing.T) {
	m := answered(t, &scriptedRuns{errs: []*view.Error{nil}}, 0)
	if got := m.tiles[1].pace(); got != plugin.PaceLocal {
		t.Fatalf("the fixture is paced at %s, want the host's own %s", got, plugin.PaceLocal)
	}
	ageAnswer(m, 4*time.Second)
	if strings.Contains(plain(drawn(m, 60)), "as of") {
		t.Error("an answer four seconds old says when it was")
	}
	ageAnswer(m, 20*time.Second)
	if !strings.Contains(plain(drawn(m, 60)), "as of") {
		t.Error("an answer twenty seconds old on a five-second tile says nothing")
	}
}

// Yesterday's 14:02 is not today's.
func TestAnAnswerFromAnotherDayNamesTheDay(t *testing.T) {
	m := answered(t, &scriptedRuns{errs: []*view.Error{nil}}, time.Minute)
	ageAnswer(m, 50*time.Hour)
	at := rawOf(m).at
	if want := "as of " + at.Format("Jan 2 15:04"); !strings.Contains(plain(drawn(m, 60)), want) {
		t.Errorf("the panel does not say %q:\n%s", want, plain(drawn(m, 60)))
	}
}

// A tile a third of a screen wide has no room for the whole sentence beside its
// name: it gives up words, and keeps the time and the fact of the failure.
func TestTheTimeSurvivesATileOnlyAThirdOfTheScreenWide(t *testing.T) {
	m := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	at := stamp(rawOf(m).goodAt)
	for width, want := range map[int]string{60: "as of " + at + " · refresh failed", 38: at + " · failed"} {
		panel := drawn(m, width)
		if text := plain(strings.SplitN(panel, "\n", 2)[0]); !strings.Contains(text, want) {
			t.Errorf("at %d cells the border does not say %q:\n%s", width, want, text)
		}
		assertExact(t, panel, width, 8)
	}
}

// The pause is its own wording, and the account that was refused is what to read:
// not a dimmed answer from before it.
func TestAPausedTileKeepsItsPauseWordingAndNotItsOldAnswer(t *testing.T) {
	m := answered(t, &scriptedRuns{errs: []*view.Error{nil, refused(), refused()}}, 0)
	m = askedAgain(t, askedAgain(t, m))
	if !isPaused(m) {
		t.Fatal("the fixture's tile is not paused")
	}
	// The box wraps a sentence where it likes, so what it says is read as words.
	text := strings.Join(strings.Fields(plain(strings.Join(tileLines(m.asReturned(1), 60, false, everyLine), "\n"))), " ")
	if !strings.Contains(text, "paused — credentials refused") {
		t.Errorf("a paused tile:\n%s", text)
	}
	if strings.Contains(plain(drawn(m, 60)), "as of") {
		t.Errorf("a paused tile says when its old answer was:\n%s", plain(drawn(m, 60)))
	}
}

// What a tile last knew was about the connection it ran against. After the
// environment moved, the previous one's numbers are not the fallback for the
// new one's failure: dimmed and timed they would still be the wrong destination.
func TestAChangedConnectionTakesTheKeptAnswerAway(t *testing.T) {
	m := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	if !strings.Contains(plain(drawn(m, 60)), "as of") {
		t.Fatal("the fixture's tile is not drawn from a kept answer")
	}
	before := m.tileRaw
	m.connectionChanged("")
	if rawOf(m).good != nil || !rawOf(m).goodAt.IsZero() {
		t.Error("the answer from before the connection changed is still kept")
	}
	if before[rawKey(m.tiles[1])].good == nil {
		t.Error("the forgetting wrote into the map a copy of the model shares")
	}
	text := plain(drawn(m, 60))
	if !strings.Contains(text, "db.conn.timeout") || strings.Contains(text, "as of") {
		t.Errorf("the tile after the connection changed:\n%s", text)
	}
	other := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	other.connectionChanged("some-other-profile")
	if rawOf(other).good == nil {
		t.Error("another connection changing took this tile's kept answer away")
	}
}

// The tile's height is what it shows: a kept answer is the view the row was
// sized from, so the row does not jump when a run fails.
func TestAFailedRefreshDoesNotResizeTheRow(t *testing.T) {
	ok := answered(t, &scriptedRuns{errs: []*view.Error{nil}}, 0)
	failed := askedAgain(t, answered(t, &scriptedRuns{errs: []*view.Error{nil, timedOut()}}, 0))
	if a, b := tileRowHeight(ok.asReturned(1), 60), tileRowHeight(failed.asReturned(1), 60); a != b {
		t.Errorf("the row is %d lines tall with the answer and %d with the answer kept", a, b)
	}
}

// **Opening the TUI re-spent every tile's budget.** The first run of a tile
// ignored its pace, so each launch ran everything at once: an OSV lookup per
// dependency, a handshake per host, a kubectl audit. A capability that says its
// answer moves by the hour is not asked for it at launch, and says so.
func TestATileThatDeclaresAnHourOrMoreIsNotRunWhenTheTUIOpens(t *testing.T) {
	script := &scriptedRuns{errs: []*view.Error{nil}}
	m := launched(t, script, 2*time.Hour)
	now := time.Now()
	if m.tiles[1].due(now) {
		t.Fatal("a tile paced at two hours is due at launch")
	}
	refreshTiles(m.tiles, m.tickGen, nil, nil)
	if !m.tiles[1].lastFired.IsZero() || m.tiles[1].inFlight {
		t.Error("the launch refresh dispatched a run of a tile that declares two hours")
	}

	body := plain(strings.Join(tileLines(m.asReturned(1), 38, false, everyLine), "\n"))
	if !strings.Contains(body, "not run yet — r runs it") || strings.Contains(body, "loading") {
		t.Errorf("the deferred tile says:\n%s", body)
	}
	if got, want := fgBefore(t, strings.Join(tileLines(m.asReturned(1), 38, false, everyLine), "\n"), "not run yet"), colourOf(t, theme.Subtle); got != want {
		t.Errorf("the sentence is drawn in %s, want the muted %s", got, want)
	}

	if m.tiles[1].due(now.Add(2*time.Hour - time.Minute)) {
		t.Error("the tile is due before its first pace has elapsed")
	}
	if !m.tiles[1].due(now.Add(2*time.Hour + time.Minute)) {
		t.Error("the tile is not due once its first pace has elapsed")
	}
}

func TestATileWithAShorterPaceLaunchesAsToday(t *testing.T) {
	for name, refresh := range map[string]time.Duration{
		"the host's own": 0, "thirty seconds": 30 * time.Second, "ten minutes": 10 * time.Minute, "just under an hour": 59 * time.Minute,
	} {
		m := launched(t, &scriptedRuns{errs: []*view.Error{nil}}, refresh)
		if !m.tiles[1].due(time.Now()) {
			t.Errorf("%s: the tile is not run at launch", name)
		}
		if body := plain(strings.Join(tileLines(m.asReturned(1), 38, false, everyLine), "\n")); strings.Contains(body, "not run yet") {
			t.Errorf("%s: the tile says it was not run:\n%s", name, body)
		}
	}
}

// r is the person asking: the tile runs at once, and is a tile like any other
// from then on.
func TestRRunsATileThatWasNotRunAtLaunch(t *testing.T) {
	script := &scriptedRuns{errs: []*view.Error{nil}}
	m := launched(t, script, 2*time.Hour)
	m.selected = 1
	next, cmd, handled := m.dashboardKeys(keyMsg("r"))
	if !handled || cmd == nil {
		t.Fatalf("r on a deferred tile handled=%v with a command %v", handled, cmd != nil)
	}
	m = next.(Model)
	if m.tiles[1].deferred(time.Now()) || m.tiles[1].lastFired.IsZero() {
		t.Error("r did not release the tile and run it")
	}
	m = askedAgain(t, m)
	if text := plain(drawn(m, 60)); strings.Contains(text, "not run yet") || !strings.Contains(text, "up") {
		t.Errorf("the tile after r:\n%s", text)
	}
}

// Nothing but the person or the clock lifts it: the environment settling a
// moment after launch used to make every tile due again, and that is the same
// burst by another door.
func TestAnEnvironmentSettlingDoesNotRunADeferredTile(t *testing.T) {
	m := launched(t, &scriptedRuns{errs: []*view.Error{nil}}, 2*time.Hour)
	resetDue(m.tiles, "")
	if m.tiles[1].due(time.Now()) {
		t.Error("the environment landing launched a tile that declares two hours")
	}
	m.rebuildTiles()
	if m.tiles[1].due(time.Now()) {
		t.Error("rebuilding the dashboard launched a tile that declares two hours")
	}
}

func TestTheFooterOffersRToRunADeferredTileAndNotToAskAnotherAgain(t *testing.T) {
	m := launched(t, &scriptedRuns{errs: []*view.Error{nil}}, 2*time.Hour)
	m.selected = 1
	bar := footerOf(m.dashFooter(), 2)
	if !strings.Contains(bar, "r run") || strings.Contains(bar, "ask again") {
		t.Errorf("the footer on a deferred tile:\n%s", bar)
	}
	m.selected = 0
	if bar := footerOf(m.dashFooter(), 2); strings.Contains(bar, "r run") {
		t.Errorf("the footer offers r on the search line:\n%s", bar)
	}
}

// A deferred tile has said all it will: a header that read it as one still
// loading would say so for the hour.
func TestADeferredTileIsNotCountedAsLoading(t *testing.T) {
	m := launched(t, &scriptedRuns{errs: []*view.Error{nil}}, 2*time.Hour)
	if g, answered := tileGrade(m.asReturned(1)); g != gradeNone || !answered {
		t.Errorf("a deferred tile grades %v answered %v, want nothing graded and said", g, answered)
	}
}
