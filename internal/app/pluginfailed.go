package app

import (
	"github.com/this-is-tobi/rta/internal/pluginhost"
)

// failedPluginsFound is what discovery found, trusted, and could not start.
//
// Carried the way untrustedPluginsFound is, for the same reason read the other
// way round. A trust gate's failure mode is silence, and this one's was the
// opposite — two lines of stderr before every command — followed by the
// silence of the inventories: `rta plugin list` and `rta doctor` listed what
// had loaded, so a plugin that was installed, approved and broken was
// indistinguishable there from one that was never installed, and the only
// place its name appeared was a line that had scrolled past.
var failedPluginsFound []pluginhost.Failed

// SetFailedPlugins records what a host found and could not start.
func SetFailedPlugins(fs []pluginhost.Failed) { failedPluginsFound = fs }

// failedToStartStatus is the word the Can column and the doctor row say it
// with. It leads with "fail", which the status palette draws as an error.
const failedToStartStatus = "failed to start"

// failedPluginDetail is what the inventory says of a plugin that did not
// start: how it ended, and the command that takes it out of the way.
func failedPluginDetail(f pluginhost.Failed) string {
	detail := f.Reason
	if f.Remedy != "" {
		detail += " — " + f.Remedy
	}
	return detail
}

// doctorFailedPlugins is the doctor row for each trusted plugin that did not
// start. A warning rather than an error: nothing about the boundary is wrong,
// a plugin the operator installed is not being provided, and the row carries
// the way out beside the reason.
func doctorFailedPlugins(add func(check, status, detail string)) {
	for _, f := range failedPluginsFound {
		add("plugin "+f.Name, "warn", f.Path+" ("+f.Short()+") "+failedToStartStatus+": "+failedPluginDetail(f))
	}
}
