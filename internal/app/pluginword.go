package app

import (
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// pluginWordHint is plugindist.WordHint over what discovery found this run: a
// word that is the name of a plugin which is not running, said the one way
// every surface says it.
//
// For the root command's unknown-word path, which has to do two things with
// it: take the neighbours back — `rta pg` was told it meant "pkg", `rta docker`
// "doctor" and `rta qdrant` "grant", names that share nothing with the word but
// its length — and say what the word is instead. ok is false for every other
// word, and the path keeps whatever it said before. `rta k8s` with kube
// installed is a typo for `rta kube`, and being told to install it is the one
// answer that is wrong, which is what loaded is for.
//
// The untrusted and failed answers come before the first-party one in
// WordHint, here and not in each caller: `rta explain pg.status`, a grant
// target and a profile's plugin key all come through this, and so does the
// dashboard's search, which kept a copy of the first-party half.
func pluginWordHint(loaded func(namespace string) bool, word string) (hint string, ok bool) {
	return plugindist.WordHint(word, loaded, untrustedPluginsFound, failedPluginsFound)
}

// missingPluginHint is pluginWordHint for a capability ID, a grant target or
// a plugin key, which carry the plugin's name in front of a dot: what the
// registered capabilities are is how it knows the plugin is there.
func missingPluginHint(registered []plugin.Capability, target string) (hint string, ok bool) {
	return pluginWordHint(loadedIn(registered), plugin.Namespace(target))
}

// loadedIn is the loaded test for a caller that holds the capabilities
// registered: a plugin is running when something is registered under its name.
func loadedIn(registered []plugin.Capability) func(namespace string) bool {
	return func(namespace string) bool {
		for _, c := range registered {
			if plugin.Namespace(c.ID) == namespace {
				return true
			}
		}
		return false
	}
}

// commandsOf is the loaded test for the root command's unknown-word path: a
// plugin that is running has a command of its own at the root.
func commandsOf(root *cobra.Command) func(namespace string) bool {
	return func(namespace string) bool {
		for _, c := range root.Commands() {
			if c.Name() == namespace {
				return true
			}
		}
		return false
	}
}
