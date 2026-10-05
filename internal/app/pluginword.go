package app

import (
	"github.com/spf13/cobra"

	"github.com/this-is-tobi/rta/internal/plugindist"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// pluginWordHint answers a word typed where a plugin's name goes — a command,
// a capability's namespace, a profile's plugin key — that is the name of a
// plugin: one rta knows to be first-party and finds nowhere on this machine,
// or one that is installed and did not start.
//
// For the root command's unknown-word path, which has to do two things with
// it: take the neighbours back — `rta pg` was told it meant "pkg", `rta docker`
// "doctor" and `rta qdrant` "grant", names that share nothing with the word but
// its length — and say what the word is instead. ok is false for every other
// word, and the path keeps whatever it said before.
//
// loaded is how the caller knows a plugin is already running, and a word
// whose plugin is gives no answer: `rta k8s` with kube installed is a typo for
// `rta kube`, and being told to install it is the one answer that is wrong.
//
// After the check for a plugin that is on $PATH and waiting for approval, never
// before it: a first-party name that is installed and untrusted is not a plugin
// to install, and the one-line answer for it is `rta plugin trust`.
func pluginWordHint(loaded func(namespace string) bool, word string) (hint string, ok bool) {
	for _, f := range failedPluginsFound {
		if f.Name == word {
			return word + " is installed and failed to start — `rta plugin list` says why", true
		}
	}
	if hint := plugindist.FirstPartyHint(word, loaded); hint != "" {
		return hint, true
	}
	return "", false
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
