package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

func podTile(source tileSource, namespace string) tile {
	t := tile{cap: plugin.Capability{ID: "kube.pod.list"}, profile: "ohmlab", source: source}
	if namespace != "" {
		t.values = map[string]any{"namespace": namespace}
	}
	return t
}

func namespacesOf(tiles []tile) []string {
	out := make([]string, len(tiles))
	for i, t := range tiles {
		out[i], _ = t.values["namespace"].(string)
	}
	return out
}

// The key of a tile names its capability and its connection and not what it is
// about, so a stated list may hold nine kube.pod.list tiles of one profile. An
// added entry for a tenth took the place of the last of the nine, and the
// screen drew one tile fewer than it said it held, with nothing said.
func TestAnAddedTileOfAnotherInputJoinsTheStatedOnesAndReplacesNone(t *testing.T) {
	stated := []tile{podTile(tileStated, "a"), podTile(tileStated, "b"), podTile(tileStated, "c")}
	got := joinTiles(stated, []tile{podTile(tileAdded, "d")})
	want := []string{"a", "b", "c", "d"}
	if names := namespacesOf(got); len(names) != len(want) || names[0] != "a" || names[1] != "b" || names[2] != "c" || names[3] != "d" {
		t.Fatalf("namespaces = %v, want %v", names, want)
	}
}

// What the replacement is for stays: an added entry that asks about the same
// thing as a stated tile is that tile, in its place, and is not drawn twice.
func TestAnAddedTileOfTheSameInputsTakesTheStatedOnesPlace(t *testing.T) {
	stated := []tile{podTile(tileStated, "a"), podTile(tileStated, "b")}
	added := podTile(tileAdded, "a")
	added.width = 12
	got := joinTiles(stated, []tile{added})
	if len(got) != 2 {
		t.Fatalf("%d tiles, want the two stated ones", len(got))
	}
	if got[0].source != tileAdded || got[0].width != 12 || got[1].source != tileStated {
		t.Errorf("the added entry did not take the first tile's place: %+v", got)
	}
}

// The automatic tile has no inputs of its own to differ in: an entry that
// states some (`rta dashboard add`) still takes its place, as it always has.
func TestAnAddedTileTakesTheAutomaticOnesPlaceWhateverItStates(t *testing.T) {
	auto := []tile{podTile(tileAuto, "")}
	got := joinTiles(auto, []tile{podTile(tileAdded, "payments")})
	if len(got) != 1 || got[0].source != tileAdded {
		t.Fatalf("tiles = %+v, want the added one alone", got)
	}
}

// Two added entries of one key that differ in their inputs are two tiles; the
// same entry twice is one.
func TestAddedTilesAreJoinedByTheirInputsToo(t *testing.T) {
	got := joinTiles(nil, []tile{podTile(tileAdded, "a"), podTile(tileAdded, "b"), podTile(tileAdded, "a")})
	if names := namespacesOf(got); len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Fatalf("namespaces = %v, want [a b]", names)
	}
}
