package tui

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rta/internal/config"
	"github.com/this-is-tobi/rta/internal/registry"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

func TestATileWithNoDeclaredPaceIsDueOnEveryTick(t *testing.T) {
	now := time.Now()
	tl := tile{cap: plugin.Capability{ID: "sys.load", Safety: plugin.Read}, lastFired: now}
	if !tl.due(now) {
		t.Error("a tile whose capability declared no Refresh should run on every tick, as every tile always did")
	}
}

func TestATileWithAPaceWaitsItOutFromItsLastDispatch(t *testing.T) {
	now := time.Now()
	tl := tile{cap: plugin.Capability{ID: "eol.watch", Safety: plugin.Read, Refresh: 2 * time.Hour}}
	if !tl.due(now) {
		t.Fatal("a tile that has never run is due")
	}
	tl.lastFired = now
	if tl.due(now.Add(time.Hour)) {
		t.Error("an hour into a two-hour pace the tile ran again")
	}
	if !tl.due(now.Add(2 * time.Hour)) {
		t.Error("two hours in, the tile did not run")
	}
}

func TestTheSearchTileIsNeverDue(t *testing.T) {
	if (tile{search: true}).due(time.Now()) {
		t.Error("the search tile has nothing to run")
	}
}

func TestRefreshTilesSkipsATileInsideItsPaceAndStampsTheOnesItFires(t *testing.T) {
	tiles := []tile{
		{search: true},
		{cap: plugin.Capability{ID: "sys.load", Safety: plugin.Read}},
		{cap: plugin.Capability{ID: "eol.watch", Safety: plugin.Read, Refresh: 2 * time.Hour}},
	}
	_ = refreshTiles(tiles, 1, nil, nil)
	first := tiles[2].lastFired
	if first.IsZero() || tiles[1].lastFired.IsZero() {
		t.Fatalf("the first round fires everything: stamps = %v %v", tiles[1].lastFired, tiles[2].lastFired)
	}
	if !tiles[0].lastFired.IsZero() {
		t.Error("the search tile was stamped as run")
	}

	unpaced := tiles[1].lastFired
	time.Sleep(time.Millisecond)
	tiles[1].inFlight = false // its answer arrived
	_ = refreshTiles(tiles, 2, nil, nil)
	if !tiles[2].lastFired.Equal(first) {
		t.Error("the paced tile was fired again on the very next tick")
	}
	if !tiles[1].lastFired.After(unpaced) {
		t.Error("the unpaced tile was not fired on the next tick")
	}

	resetDue(tiles, "")
	_ = refreshTiles(tiles, 3, nil, nil)
	if tiles[2].lastFired.Equal(first) {
		t.Error("after resetDue the paced tile should fire regardless of its pace")
	}
}

// A tile whose run has not answered is not run again, however long its pace says
// it has waited: a slow service was being asked once more at every tick.
func TestATileThatHasNotAnsweredIsNotRunAgain(t *testing.T) {
	now := time.Now()
	tl := tile{cap: plugin.Capability{ID: "sys.load", Safety: plugin.Read}, lastFired: now, inFlight: true}
	if tl.due(now.Add(tileRefreshInterval)) {
		t.Error("a tile still waiting for its answer was run again")
	}
	if !tl.due(now.Add(refreshTimeout + 2*tileRefreshInterval)) {
		t.Error("a run that never answered kept its tile from running for ever")
	}
	tl.inFlight = false
	if !tl.due(now.Add(tileRefreshInterval)) {
		t.Error("once it answered the tile was not due at the next tick")
	}
}

func TestRefreshTilesMarksTheTilesItFiresAsWaitingForAnAnswer(t *testing.T) {
	tiles := []tile{{search: true}, {cap: plugin.Capability{ID: "sys.load", Safety: plugin.Read}}}
	_ = refreshTiles(tiles, 1, nil, nil)
	if !tiles[1].inFlight {
		t.Error("a dispatched tile is not marked as waiting")
	}
	first := tiles[1].lastFired
	time.Sleep(time.Millisecond)
	_ = refreshTiles(tiles, 2, nil, nil)
	if !tiles[1].lastFired.Equal(first) {
		t.Error("a tile with a run outstanding was dispatched again")
	}
	resetDue(tiles, "")
	if tiles[1].inFlight {
		t.Error("resetDue keeps a tile from firing at once, which is what it is for")
	}
}

// What a run costs follows where it goes: a capability that reaches a
// configured connection is a round trip to a service somebody else logs, and
// polling it at a clock's pace was the dashboard's worst habit.
func TestATileThatReachesAConnectionRunsAtTheRemotePaceUnlessItDeclaredOne(t *testing.T) {
	host := plugin.Field{Name: "host", Type: plugin.String, Config: "host"}
	remote := plugin.Capability{ID: "pg.status", Safety: plugin.Read, Inputs: []plugin.Field{host}}
	if got := remote.Pace(); got != plugin.PaceRemote {
		t.Fatalf("a capability that reaches a connection runs every %v, want %v", got, plugin.PaceRemote)
	}
	now := time.Now()
	tl := tile{cap: remote, lastFired: now}
	if tl.due(now.Add(10 * time.Second)) {
		t.Error("a remote tile ran ten seconds after its last run")
	}
	if !tl.due(now.Add(plugin.PaceRemote)) {
		t.Error("a remote tile was not due at its pace")
	}
	declared := remote
	declared.Refresh = time.Minute
	if got := declared.Pace(); got != time.Minute {
		t.Errorf("a declared pace was overridden: %v", got)
	}
}

// The nearest `refresh:` wins — the tile's, the section's, the screen's, then the
// capability's — and a default is told from a demand by what becomes of it under a
// capability's declared pace.
func TestResolvePaceTakesTheNearestStatementAndSaysWhereItWas(t *testing.T) {
	host := plugin.Field{Name: "host", Type: plugin.String, Config: "host"}
	remote := plugin.Capability{ID: "pg.status", Safety: plugin.Read, Inputs: []plugin.Field{host}}
	slow := plugin.Capability{ID: "eol.watch", Safety: plugin.Read, Refresh: 2 * time.Hour}
	dash := config.Dashboard{
		Refresh:  "1m",
		Sections: map[string]config.DashboardSection{"databases": {Refresh: "45s"}},
	}
	for _, tc := range []struct {
		name     string
		t        tile
		want     time.Duration
		wantFrom string
	}{
		{"the tile's own", tile{cap: remote, section: "databases", refresh: 10 * time.Second}, 10 * time.Second, "tile"},
		{"the section's", tile{cap: remote, section: "databases"}, 45 * time.Second, "section"},
		{"the screen's", tile{cap: remote}, time.Minute, "screen"},
		{"a section the screen does not state", tile{cap: remote, section: "other"}, time.Minute, "screen"},
		{"a default under a declared pace yields", tile{cap: slow, section: "databases"}, 2 * time.Hour, ""},
		{"a demand under a declared pace is cut", tile{cap: slow, refresh: time.Minute}, 2 * time.Hour, ""},
		{"slower than a declared pace is allowed", tile{cap: slow, refresh: 6 * time.Hour}, 6 * time.Hour, "tile"},
		{"under the tick", tile{cap: remote, refresh: time.Second}, plugin.PaceLocal, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, from := resolvePace(tc.t, dash)
			if got != tc.want || from != tc.wantFrom {
				t.Errorf("resolvePace = %v from %q, want %v from %q", got, from, tc.want, tc.wantFrom)
			}
		})
	}
	if got, from := resolvePace(tile{cap: remote}, config.Dashboard{}); got != plugin.PaceRemote || from != "" {
		t.Errorf("with nothing stated a connection runs at %v from %q", got, from)
	}
}

// A tile is run again once a call has changed what it shows: `a` on the notes
// tile added a note, and the tile went on saying "Nothing here yet" until its
// thirty seconds were up.
func TestAChangeMakesTheTilesOfItsPluginDue(t *testing.T) {
	now := time.Now()
	tiles := []tile{
		{search: true},
		{cap: plugin.Capability{ID: "note.list", Safety: plugin.Read}, lastFired: now},
		{cap: plugin.Capability{ID: "note.tags", Safety: plugin.Read}, lastFired: now, inFlight: true},
		{cap: plugin.Capability{ID: "kv.overview", Safety: plugin.Read}, lastFired: now},
		{cap: plugin.Capability{ID: "note.overdue", Safety: plugin.Read}, lastFired: now, refused: credentialRefusals},
	}
	staleAfter(tiles, plugin.Capability{ID: "note.add", Safety: plugin.Write, Flash: true})
	if !tiles[1].lastFired.IsZero() {
		t.Error("the notes tile was not made due by a note added")
	}
	if tiles[2].lastFired.IsZero() {
		t.Error("a tile waiting for its answer was made due, which would run it twice")
	}
	if tiles[3].lastFired.IsZero() == true {
		t.Error("another plugin's tile was made due")
	}
	if tiles[4].lastFired.IsZero() {
		t.Error("a paused tile was made due, which the pause exists to stop")
	}

	tiles[3].lastFired = now
	staleAfter(tiles, plugin.Capability{ID: "kv.get", Safety: plugin.Write, Reveals: true})
	if tiles[3].lastFired.IsZero() {
		t.Error("a reveal, which changes nothing, made the store's tile due")
	}
	staleAfter(tiles, plugin.Capability{ID: "kv.edit", Safety: plugin.Write, Reveals: true, Flash: true})
	if !tiles[3].lastFired.IsZero() {
		t.Error("an edit of a stored value did not make the store's tile due")
	}
	tiles[3].lastFired = now
	staleAfter(tiles, plugin.Capability{ID: "kv.list", Safety: plugin.Read})
	if tiles[3].lastFired.IsZero() {
		t.Error("a read made a tile due")
	}
}

// Through the model: the answer of a change marks the tiles, and a failed one
// does not, since nothing changed.
func TestTheAnswerOfAChangeMakesItsTilesDue(t *testing.T) {
	m := New(registry.New(), config.Dashboard{}, nil)
	now := time.Now()
	m.tiles = []tile{{cap: plugin.Capability{ID: "note.list", Safety: plugin.Read}, lastFired: now}}
	add := plugin.Capability{ID: "note.add", Safety: plugin.Write, Flash: true}
	failed, _ := m.Update(resultMsg{cap: add, err: view.Errorf("note.write", "disk full")})
	if failed.(Model).tiles[0].lastFired.IsZero() {
		t.Error("a change that failed made its tiles due")
	}
	done, _ := m.Update(resultMsg{cap: add, view: view.Text{Body: "added note 1: milk"}})
	if !done.(Model).tiles[0].lastFired.IsZero() {
		t.Error("the answer of a note added left the notes tile waiting out its pace")
	}
}
