package tui

// What a tile says about its own state, read from what it returned.

// notHere names the error codes that mean a tile has nothing to say from where
// rta was started, which is a different thing from something being broken, with
// the sentence the tile draws in place of an error badge.
//
// git.overview is the case that made the list: the landing screen is opened
// from wherever the shell happens to be, which is usually not a repository, and
// the first thing a newcomer saw was a red ERROR badge and a hint about bare
// repositories on a tile they never asked for. The sentence is muted, the way an
// empty notebook's is, and the full error, with its hint, is what enter opens.
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
