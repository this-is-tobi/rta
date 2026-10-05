package tui

import (
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/pkg/view"
)

// What a tile says about its own state, read from what it returned.

// notHere names the error codes that mean a tile has nothing to say from where
// rta was started, which is a different thing from something being broken, with
// the sentence the tile draws in place of an error badge.
//
// git.overview is the case that made the list: the landing screen is opened
// from wherever the shell happens to be, which is usually not a repository, and
// the first thing a newcomer saw was a red ERROR badge and a hint about bare
// repositories on a tile they never asked for. The sentence is muted, the way an
// empty notebook's is.
//
// Host-side, with the rest of what the landing screen decides (leftOff,
// preferredTile): the capability's error is right for `rta git overview`, which
// is asked about a directory, and wrong for a glance that was never asked.
var notHere = map[string]string{
	"git.notarepo": "Not inside a git repository — start rta in a checkout and this tile fills in.",
}

// quietSentence is what a tile draws instead of an error when it asked about
// "here" and here has no answer. A tile that was given inputs asked about
// something particular, and a particular directory that is not a repository is
// a mistake worth a badge.
func quietSentence(t tile) (string, bool) {
	if t.err == nil || len(t.values) > 0 {
		return "", false
	}
	say, ok := notHere[t.err.Code]
	return say, ok
}

// waitingKey is the label of the line agent.overview puts a count of parked
// calls under. The dashboard reads the number off it rather than asking the
// agent plugin, which it knows nothing else about; TestTheAgentTilesWaitingLineIsTheOneTheDashboardReads
// holds the two spellings together.
const waitingKey = "waiting on you"

// waitingCalls is how many calls the tile says are parked waiting for an
// answer, 0 for a tile that says none, says nothing about it, or cannot read
// the queue — "unreadable — …" is not a count, and a badge for it would be the
// dashboard asserting what the tile did not.
func waitingCalls(v view.View) int {
	kv, ok := v.(view.KeyValue)
	if !ok {
		return 0
	}
	for _, p := range kv.Pairs {
		if p.Key != waitingKey {
			continue
		}
		digits, _, _ := strings.Cut(p.Value, " ")
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
