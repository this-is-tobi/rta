package pluginhost

import (
	"path/filepath"
	"slices"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// ConfineCommand is the argument rta is run with to become a plugin under
// strict confinement on Linux (ConfineMain): main hands it over before doing
// anything else, and wrap is what puts it on a command line.
const ConfineCommand = "__confine"

// namedFile is the refusal of a call to capability id that names a file a
// plugin under `confine: strict` cannot open, or nil.
//
// **What a plugin may open is fixed when it starts, and it starts once for
// many calls** (Host): the file a call names arrives after the one moment it
// could have been allowed, and a Landlock domain only ever narrows. Left to
// the plugin, the open fails with the kernel's "permission denied", which the
// plugin reports as its own failure on the box that named the file. Read that
// way it is a directory the operator cannot write, it sends them to chmod one,
// and it says nothing of the setting that refused it. Refused here, before
// the call, the refusal names the setting.
//
// What passes is a file already there, inside a location the plugin's grant
// made readable (Readable). Nothing a call names can be created, since strict
// leaves a plugin no place to write but its own directory, which kube's
// `--out ~/.kube/<name>` would otherwise only find out from the kernel. A
// file under the system's own directories is refused too, though the plugin
// could read it: "the grant covers it, or the call does not run" is a rule an
// operator can predict, and a restore from /usr is not a call anybody makes.
func (d DenySet) namedFile(id string, inputs []plugin.Field, req plugin.Request) *view.Error {
	if !d.Strict {
		return nil
	}
	for _, f := range inputs {
		named := req.String(f.Name)
		if f.Type != plugin.Path || named == "" || d.readable(plugin.ExpandHome(named)) {
			continue
		}
		sf := req.Surface()
		name := sf.InputName(f.Name)
		if f.Positional {
			name = sf.ArgumentName(f.Name)
		}
		hint := "strict fixes what a plugin may open when it starts, before any call names a file, so a call can " +
			"hand it only a file already there in a location `rta plugin access add` gave it; this one runs only " +
			"where the configuration leaves `confine` unset"
		if sf == plugin.SurfaceMCP {
			hint = "this server starts its plugins under `confine: strict`, which fixes what one may open before " +
				"any call names a file, so no call can hand it a file of its own; that is the operator's " +
				"configuration to change, not the call's"
		}
		return view.Refusef("plugin.confine.file", "%s names %s, which `confine: strict` does not let %s open",
			name, named, plugin.Namespace(id)).WithHint(hint).WithInput(f.Name)
	}
	return nil
}

// readable is whether path is already there under one of Readable, judged by
// where it leads rather than how it is spelled. The kernel follows a link
// before Landlock looks at what it reached, so a link in a granted location
// that leads out of it is refused at the open, and one elsewhere that leads
// in is not; a check that read the spelling would refuse what works and pass
// what the open then refuses.
func (d DenySet) readable(path string) bool {
	at, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if at, err = filepath.Abs(at); err != nil {
		return false
	}
	return slices.ContainsFunc(d.Readable, func(granted string) bool {
		return slices.ContainsFunc(withTarget(granted), func(form string) bool { return inside(at, form) })
	})
}
