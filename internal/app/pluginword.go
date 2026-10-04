package app

import (
	"github.com/this-is-tobi/rta/internal/plugindist"
)

// pluginWordHint answers a word typed where a command goes that is the name of
// a plugin: one rta knows to be first-party and finds nowhere on this machine,
// or one that is installed and did not start.
//
// For the root command's unknown-word path, which has to do two things with
// it: take the neighbours back — `rta pg` was told it meant "pkg", `rta docker`
// "doctor" and `rta qdrant` "grant", names that share nothing with the word but
// its length — and say what the word is instead. ok is false for every other
// word, and the path keeps whatever it said before.
//
// After the check for a plugin that is on $PATH and waiting for approval, never
// before it: a first-party name that is installed and untrusted is not a plugin
// to install, and the one-line answer for it is `rta plugin trust`.
func pluginWordHint(word string) (hint string, ok bool) {
	for _, f := range failedPluginsFound {
		if f.Name == word {
			return word + " is installed and failed to start — `rta plugin list` says why", true
		}
	}
	if hint := plugindist.FirstPartyHint(word); hint != "" {
		return hint, true
	}
	return "", false
}
