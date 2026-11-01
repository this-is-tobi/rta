package pluginhost

import (
	"errors"
	"path/filepath"
	"regexp"
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

// deniedOpen is err with the setting that refused it named, where err is the
// failure of a plugin, launched from exe, to open a file `confine: strict`
// keeps from it.
//
// namedFile refuses such a file before the call when the input naming it is a
// Path. One that is not — a TLS file a profile fills, which a Path never is
// (plugin.ProfileFillable), or a file the plugin finds for itself — reaches
// the kernel, and its "permission denied" came back as the plugin's own
// failure under the plugin's own hint, "a path on this machine", which reads
// as a chmod and says nothing of the setting. The plugin cannot tell the
// policy's refusal from the file's own mode, and its failure crosses the wire
// as text, so the path is read from the words Go gives a failed open
// (pathDenied), and the setting is named only where the launch's policy keeps
// that path from the plugin. What the policy allows, something else refused,
// and the plugin's hint stands.
func (d DenySet) deniedOpen(id, exe string, req plugin.Request, err error) error {
	if !d.Strict {
		return err
	}
	return explainDenied(id, req.Surface(), err, func(path string) bool { return d.strictAllows(exe, path) })
}

// explainDenied is deniedOpen's reading of err, with allows standing for the
// launch's policy.
func explainDenied(id string, sf plugin.Surface, err error, allows func(path string) bool) error {
	var verr *view.Error
	if !errors.As(err, &verr) {
		return err
	}
	for _, m := range pathDenied.FindAllStringSubmatch(verr.Message, -1) {
		if allows(m[1]) {
			continue
		}
		ns := plugin.Namespace(id)
		hint := "`confine: strict` keeps " + ns + " from opening " + m[1] + ": a plugin under it opens the " +
			"system's directories, /etc and /opt among them, its own temporary directory, the tools on its PATH, " +
			"the credential locations `rta plugin access add` gave it and the paths you named for it, and " +
			"nothing else — `rta plugin access add " + ns + " --path <its directory>` lets it read the file where it is"
		if sf == plugin.SurfaceMCP {
			hint = "this server starts its plugins under `confine: strict`, which keeps " + ns + " from opening " +
				m[1] + "; that is the operator's configuration to change, not the call's"
		}
		out := *verr
		out.Hint = hint
		return &out
	}
	return err
}

// pathDenied is the words Go gives an open, a stat or an exec the kernel
// refused (*fs.PathError), "open /srv/ca.pem: permission denied", with the path
// as its one group. EACCES is what Landlock answers a path it does not allow
// with, and a plugin's message keeps the words when it wraps the error with %v,
// as the official plugins' do.
var pathDenied = regexp.MustCompile(`\b[a-z]+ (/[^\n]*?): permission denied`)

// readable is whether path is already there under one of Readable, judged by
// where it leads rather than how it is spelled. The kernel follows a link
// before Landlock looks at what it reached, so a link in a granted location
// that leads out of it is refused at the open, and one elsewhere that leads
// in is not; a check that read the spelling would refuse what works and pass
// what the open then refuses.
//
// And not one of rta's own, which the policy carves out of a granted location
// (strictPolicy): a store's key kept in a granted ~/.ssh passed here, so the
// call naming it was handed to the plugin, whose open the kernel refused, and
// the person read the plugin's "permission denied" on the box that named the
// file where plugin.confine.file would have said why.
func (d DenySet) readable(path string) bool {
	at, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}
	if at, err = filepath.Abs(at); err != nil {
		return false
	}
	if slices.ContainsFunc(d.ownFiles(), func(own string) bool { return inside(at, own) }) {
		return false
	}
	under := func(roots []string) bool {
		return slices.ContainsFunc(roots, func(granted string) bool {
			return slices.ContainsFunc(withTarget(granted), func(form string) bool { return inside(at, form) })
		})
	}
	if under(d.Readable) {
		return true
	}
	// A path the operator named, less the credential locations this artifact
	// was not given, which the policy carves out of it.
	return under(d.Named) && !under(d.NoRead)
}

// ownFiles is rta's own state as strict keeps it out of every location it
// allows: what the deny set refuses outright, and what of rta's own the
// operator keeps where the macOS profile cannot name it (Unnamed), each with
// where it leads. One list for the policy and for the gate in front of it, so
// the two cannot come to disagree about what is rta's.
func (d DenySet) ownFiles() []string {
	own := slices.Clone(d.NoAccess)
	for _, f := range d.Unnamed {
		own = append(own, withTarget(f)...)
	}
	return own
}
