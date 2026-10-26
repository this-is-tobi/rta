package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// scriptedRuns is what a database tile says on each run, in order, the last one
// again once they run out.
type scriptedRuns struct {
	errs []*view.Error
	runs int
}

func (a *scriptedRuns) next() (view.View, error) {
	e := a.errs[min(a.runs, len(a.errs)-1)]
	a.runs++
	if e == nil {
		return view.Text{Body: "up"}, nil
	}
	return nil, e
}

func refused() *view.Error {
	return view.Errorf("db.auth.failed", "db.example:5432 rejected the credentials for %q", "nobody").
		WithHint("the password is read from the profile")
}

func timedOut() *view.Error {
	return view.Errorf("db.conn.timeout", "db.example:5432 did not answer in time")
}

// dbDashboard is a dashboard of one tile, db.status, whose runs are scripted.
func dbDashboard(t *testing.T, script *scriptedRuns) Model {
	t.Helper()
	t.Setenv("RTA_CONFIG", t.TempDir()+"/config.yaml")
	reg := registry.New()
	if err := reg.Register(plugin.Plugin{Name: "db", Summary: "db", Capabilities: []plugin.Capability{{
		ID: "db.status", Summary: "status", Safety: plugin.Read,
		Run: func(context.Context, plugin.Request) (view.View, error) { return script.next() },
	}}}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, config.Dashboard{Tiles: []config.Tile{{ID: "db.status"}}}, nil)
	m = filled(t, m, 80, 24)
	if len(m.tiles) != 2 {
		t.Fatalf("the fixture has %d tiles, want the search and one", len(m.tiles))
	}
	return m
}

// askedAgain runs the tile once more and hands the answer to the model, as a refresh
// that came due does.
func askedAgain(t *testing.T, m Model) Model {
	t.Helper()
	next, _ := m.Update(tileCmd(1, m.tiles[1], statedConfig{}, "", nil, config.Connection{})())
	return next.(Model)
}

func isPaused(m Model) bool { return !m.tiles[1].due(time.Now().Add(time.Hour)) }

// **A tile whose credential is refused retried the login at every refresh,
// for ever.** A pg tile on a role that did not exist logged a FATAL every thirty
// seconds, 2880 a day, which on a database that locks is an account lock-out
// waiting to happen. Two refusals in a row are an answer; the third is not asked.
func TestACredentialRefusedTwiceStopsTheTileFromRefreshing(t *testing.T) {
	script := &scriptedRuns{errs: []*view.Error{refused()}}
	m := dbDashboard(t, script)
	if isPaused(m) {
		t.Fatal("the tile stopped after one refusal, which may be a typo fixed a moment later")
	}
	m = askedAgain(t, m)
	if !isPaused(m) {
		t.Fatal("two refusals in a row and the tile would still be asked again")
	}

	before := m.tiles[1].lastFired
	refreshTiles(m.tiles, m.tickGen, nil, nil)
	if !m.tiles[1].lastFired.Equal(before) {
		t.Error("a tick dispatched a run of a paused tile")
	}
	if script.runs != 2 {
		t.Errorf("the tile ran %d times, want the two that were refused", script.runs)
	}
}

// Only a refusal of the credential counts, and only two in a row: a timeout, a
// refused connection or an answer between them is a service that is there or
// not, and the tile has to keep asking to say when it is back.
func TestOnlyTwoRefusalsInARowPauseATile(t *testing.T) {
	for name, errs := range map[string][]*view.Error{
		"timeouts":                        {timedOut()},
		"a refused connection":            {view.Errorf("db.conn.refused", "nothing is listening")},
		"a refusal, an answer, a refusal": {refused(), nil, refused()},
		"a refusal, a timeout, a refusal": {refused(), timedOut(), refused()},
	} {
		t.Run(name, func(t *testing.T) {
			m := dbDashboard(t, &scriptedRuns{errs: errs})
			// The script's first answer was the dashboard's own first run, and the
			// last one repeats: a script of one is asked three times more.
			more := len(errs) - 1
			if len(errs) == 1 {
				more = 3
			}
			for range more {
				if m = askedAgain(t, m); isPaused(m) {
					t.Fatalf("the tile stopped on %s", name)
				}
			}
		})
	}
}

// The tile says why it is not moving, in the words a failed tile uses, with
// what refused and the key that asks again, and it is still a bad tile: a
// header that folds it counts it, so pausing hides nothing.
func TestAPausedTileSaysSoAndStaysBad(t *testing.T) {
	m := askedAgain(t, dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}}))
	// The box wraps a sentence where it likes, so what it says is read as words.
	body := strings.Join(strings.Fields(plain(strings.Join(tileLines(m.tiles[1], 38, false, everyLine), "\n"))), " ")
	for _, want := range []string{"paused — credentials refused", "db.auth.failed", "rejected", "r asks again"} {
		if !strings.Contains(body, want) {
			t.Errorf("the paused tile does not say %q:\n%s", want, body)
		}
	}
	if g, _ := tileGrade(m.tiles[1]); g != gradeBad {
		t.Errorf("a paused tile grades %v, want bad", g)
	}

	unpaused := dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}})
	if body := plain(strings.Join(tileLines(unpaused.tiles[1], 38, false, everyLine), "\n")); strings.Contains(body, "paused") {
		t.Errorf("a tile refused once says it is paused:\n%s", body)
	}
}

// r on the paused tile asks again at once.
func TestRAsksAPausedTileAgain(t *testing.T) {
	script := &scriptedRuns{errs: []*view.Error{refused(), refused(), nil}}
	m := askedAgain(t, dbDashboard(t, script))
	m.selected = 1
	next, cmd, handled := m.dashboardKeys(keyMsg("r"))
	if !handled || cmd == nil {
		t.Fatalf("r on a paused tile handled=%v with a command %v", handled, cmd != nil)
	}
	m = next.(Model)
	if isPaused(m) || m.tiles[1].refused != 0 {
		t.Error("r did not take the pause off")
	}
	if m = askedAgain(t, m); isPaused(m) {
		t.Error("the tile that answered after r is still paused")
	}
}

// r asks a paused tile again and does nothing for any other: the search box has
// nothing to run, and a tile that is running needs no key, which the footer
// does not offer either.
func TestROnlyAsksATileThatIsPaused(t *testing.T) {
	m := dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}})
	for name, at := range map[string]int{"the search tile": 0, "a tile that is not paused": 1} {
		m.selected = at
		next, cmd, handled := m.dashboardKeys(keyMsg("r"))
		if !handled || cmd != nil {
			t.Errorf("r on %s handled=%v with a command %v", name, handled, cmd != nil)
		}
		if got := next.(Model).tiles[1]; !got.lastFired.Equal(m.tiles[1].lastFired) || got.refused != m.tiles[1].refused {
			t.Errorf("r on %s changed the tile", name)
		}
	}
}

// The footer offers r on the tile that is paused and on no other.
func TestTheFooterOffersRToAPausedTileOnly(t *testing.T) {
	m := dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}})
	m.selected = 1
	if strings.Contains(footerOf(m.dashFooter(), 2), "ask again") {
		t.Errorf("a tile that is running is offered r:\n%s", footerOf(m.dashFooter(), 2))
	}
	m = askedAgain(t, m)
	m.selected = 1
	if !strings.Contains(footerOf(m.dashFooter(), 2), "r ask again") {
		t.Errorf("a paused tile is not offered r:\n%s", footerOf(m.dashFooter(), 2))
	}
}

// The pause is about the credential the tile read, so a connection that changed
// under it takes the pause off: the environment switched, or a profile edited.
func TestAPausedTileResumesWhenItsConnectionChanges(t *testing.T) {
	m := askedAgain(t, dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}}))
	resetDue(m.tiles, "other-profile")
	if !isPaused(m) {
		t.Error("another connection changing took this tile's pause off")
	}
	resetDue(m.tiles, "")
	if isPaused(m) {
		t.Error("the connection the tile follows changed and the tile is still paused")
	}
}

// A rebuild of the dashboard (a tile hidden, the arrangement changed) must not
// be the way to two more logins.
func TestAPausedTileStaysPausedThroughARebuild(t *testing.T) {
	m := askedAgain(t, dbDashboard(t, &scriptedRuns{errs: []*view.Error{refused()}}))
	m.rebuildTiles()
	if !isPaused(m) {
		t.Error("rebuilding the dashboard took the pause off a tile")
	}
}
