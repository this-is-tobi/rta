package tui

import (
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
)

// The keys every screen answers itself are the ones Validate refuses to a
// plugin's actions, and the two lists have to agree: a key the shell claims
// tomorrow that pkg/plugin does not know would be handed to a plugin the same
// day, and a row action shadows the screen's own binding of the same key.
// The shell's vocabulary is the source; pkg/plugin's set is held to it.
func TestTheShellsOwnKeysAreReservedToPlugins(t *testing.T) {
	reserved := plugin.ReservedActionKeys()
	for _, b := range []binding{
		bindQuit, bindBack, bindOpen, bindRerun, bindEdit, bindCopy,
		bindBrowse, bindSearch, bindSelect, bindScroll, bindColumn,
	} {
		for _, k := range b.keys {
			if len([]rune(k)) != 1 && k != "enter" && k != "esc" && k != "tab" {
				continue // ctrl+c, the arrows: nothing an action could bind anyway
			}
			if _, ok := reserved[k]; !ok {
				t.Errorf("%q (%s) is a key every screen answers, and plugin.ReservedActionKeys does not name it", k, b.label)
			}
		}
	}
}

// Every action a built-in declares opens something the registry has. The
// built-ins pass the same admission a third party would — the same
// Validate, at registration — and this is the one thing Validate cannot
// see: a target in another built-in, which only the registry resolves.
func TestEveryBuiltInActionResolves(t *testing.T) {
	reg := realRegistry(t)
	for _, c := range reg.Capabilities() {
		for _, a := range c.Actions {
			if _, ok := reg.Capability(a.Target); !ok {
				t.Errorf("%s %s opens %q, which the registry does not have", c.ID, a.Key, a.Target)
			}
		}
	}
}
