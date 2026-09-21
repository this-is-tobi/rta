package tui

import (
	"testing"
	"time"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
