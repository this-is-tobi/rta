package audit

import (
	"path/filepath"
	"strings"
)

// Grading a containerised rta against the recipe it was copied from.
//
// docs/30-boundary/20-mcp.md prints a `docker run` line with six deliberate
// parts and a table explaining each, and nothing checked whether the line in
// front of you still had them. An operator who containerised rta got the same
// silence as one who had not — which is the wrong way round, because they are
// the operator who read the hardening section and acted on it, and the one
// whose half-applied recipe looks identical to a complete one.
//
// What is graded here is only what that page states as a requirement. The
// flags it calls a choice stay ungraded, `--network none` loudest among them:
// its own row says to drop it when you want the capabilities that reach the
// network, so failing its absence would be rta arguing with its documentation
// in a report somebody is supposed to act on.

var containerRuntimes = map[string]bool{
	"docker": true, "podman": true, "nerdctl": true, "finch": true,
}

// containerLaunch splits a container-launched rta into the runtime's arguments
// and rta's own, and reports whether this declaration is one at all.
//
// The split is `mcp serve`, not the image name, because the image is the part
// that varies: the "share the image" recipe builds a derived image with an
// arbitrary name and `ENTRYPOINT ["rta"]`, and matching on `ghcr.io/...` would
// grade the published image and quietly skip every team's own — the opposite
// of useful, since the derived one is where the flags get edited.
func containerLaunch(d serverDecl) (runtimeArgs, rtaArgs []string, ok bool) {
	if !containerRuntimes[filepath.Base(d.command)] {
		return nil, nil, false
	}
	for i := 0; i+1 < len(d.args); i++ {
		if d.args[i] == "mcp" && d.args[i+1] == "serve" {
			return d.args[:i], d.args[i:], true
		}
	}
	return nil, nil, false
}

// officialImage is the published narrow image's repository, without registry
// or tag. Everything this file concludes about an image keys off it, because
// it is the one image whose contents this repository actually defines.
const officialImage = "this-is-tobi/rta"

// launchedImage is the image reference a declaration runs: the token before
// `mcp`, or the one before that when the declaration spells the binary out
// (`… image /usr/local/bin/rta mcp serve`).
func launchedImage(runtimeArgs []string) string {
	for i := len(runtimeArgs) - 1; i >= 0; i-- {
		if tok := runtimeArgs[i]; filepath.Base(tok) != "rta" {
			return tok
		}
	}
	return ""
}

// orElseImage names the image in prose, or says "the image" when the
// declaration was shaped in a way launchedImage could not read — a sentence
// with an empty spot in it reads as a bug in the audit.
func orElseImage(image string) string {
	if image == "" {
		return "the image"
	}
	return image
}

func isOfficialFull(image string) bool {
	return strings.Contains(image, officialImage+"-full")
}

// isOfficialNarrow reports the published narrow image, tag or digest ignored.
//
// This matters because it is the only image whose environment rta can state
// from here: its Dockerfile sets PATH and nothing else. Anything else — a
// private registry, the documented "share the image" build, a fork — may set
// RTA_CONFIG and RTA_DATA_DIR itself, and reading a derived image's ENV means
// pulling it, which a configuration audit has no business doing.
func isOfficialNarrow(image string) bool {
	if image == "" || isOfficialFull(image) {
		return false
	}
	name := image
	if i := strings.Index(name, "@"); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, ":"); i >= 0 && !strings.Contains(name[i:], "/") {
		name = name[:i]
	}
	return name == officialImage || strings.HasSuffix(name, "/"+officialImage)
}

// flagValues returns every value given to any of names, accepting both
// `--flag value` and `--flag=value`.
func flagValues(args []string, names ...string) []string {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []string
	for i := 0; i < len(args); i++ {
		if k, v, found := strings.Cut(args[i], "="); found && want[k] {
			out = append(out, v)
			continue
		}
		if want[args[i]] {
			if i+1 < len(args) {
				out = append(out, args[i+1])
				i++
				continue
			}
			out = append(out, "")
		}
	}
	return out
}

func hasBoolFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// hasValue reports a flag carrying one of the accepted values, compared
// case-insensitively and by prefix — `--security-opt no-new-privileges` and
// `--security-opt no-new-privileges:true` are the same decision, and refusing
// to recognise the second would tell somebody to add a flag they already have.
func hasValue(args []string, name string, accept ...string) bool {
	for _, got := range flagValues(args, name) {
		for _, want := range accept {
			if strings.HasPrefix(strings.ToLower(strings.TrimSpace(got)), want) {
				return true
			}
		}
	}
	return false
}

// mountTargets is every in-container path the declaration mounts something at,
// from -v/--volume's second field and --mount's target= key.
func mountTargets(args []string) []string {
	var out []string
	for _, v := range flagValues(args, "-v", "--volume") {
		parts := strings.Split(v, ":")
		if len(parts) >= 2 {
			out = append(out, parts[1])
		}
	}
	for _, m := range flagValues(args, "--mount") {
		for _, field := range strings.Split(m, ",") {
			if k, val, found := strings.Cut(field, "="); found &&
				(k == "target" || k == "destination" || k == "dst") {
				out = append(out, val)
			}
		}
	}
	return out
}

// mounted reports whether dir is covered by a mount — the exact path, or a
// parent of it, since mounting /rta-home covers /rta-home/state.
func mounted(args []string, dir string) bool {
	dir = filepath.Clean(dir)
	for _, t := range mountTargets(args) {
		t = filepath.Clean(t)
		if t == dir || strings.HasPrefix(dir, t+"/") {
			return true
		}
	}
	return false
}

// gradeContainer grades one container-launched rta against the documented
// recipe. It says nothing at all about a container running something else, or
// about an rta that was never containerised: the shell question is the tools
// group's, and answering it twice in two wordings helps nobody.
func gradeContainer(r *agentReport, f agentFile, name string, d serverDecl) {
	runtimeArgs, rtaArgs, ok := containerLaunch(d)
	if !ok {
		return
	}

	env := map[string]string{}
	for _, e := range flagValues(runtimeArgs, "-e", "--env") {
		if k, v, found := strings.Cut(e, "="); found {
			env[k] = v
		}
	}
	image := launchedImage(runtimeArgs)

	// Skipped entirely for rta-full, which sets both in the image: reporting a
	// declaration for not repeating what its image already does is the audit
	// inventing a problem out of not knowing what it was looking at.
	var missing []string
	if !isOfficialFull(image) {
		for _, key := range []string{"RTA_CONFIG", "RTA_DATA_DIR"} {
			if env[key] == "" {
				missing = append(missing, key)
			}
		}
	}
	if len(missing) > 0 {
		// Certainty decides the grade. The published narrow image's Dockerfile
		// is in this repository and sets PATH alone, so the paths really are
		// absent and the settings really are being ignored. Any other image —
		// a private build, the documented "share the image" recipe, a fork —
		// may set them itself, and that is the ordinary case for a private
		// deployment rather than a suspicious one. So it is told what to check
		// and never accused of a misconfiguration rta cannot see.
		if isOfficialNarrow(image) {
			r.add(grpAgentServers, name, stFail,
				"containerised without "+strings.Join(missing, " and ")+", and the published image "+
					"sets neither — with no config directory the path falls back to a "+
					"working-directory file that is not honoured, so every profile, plugin and "+
					"dashboard setting is silently ignored", refMisconfig)
		} else {
			r.add(grpAgentServers, name, stInfo,
				"does not pass "+strings.Join(missing, " or ")+", so this depends on "+
					orElseImage(image)+" setting them itself — if it does not, the config path "+
					"falls back to a working-directory file that is not honoured and every profile "+
					"and plugin setting is silently ignored", refMisconfig)
		}
		r.addFix("container-paths", name+" — make sure the container has its config and data paths",
			"Either the image sets them or the declaration does. If "+orElseImage(image)+" does "+
				"not set them, add both to the `docker run` arguments in "+shortPath(f.path)+", "+
				"pointing at the volume the container already mounts:\n\n"+
				"  \"-e\", \"RTA_CONFIG=/rta-home/config.yaml\",\n"+
				"  \"-e\", \"RTA_DATA_DIR=/rta-home\",\n\n"+
				"This is the failure that looks like success: nothing errors, the server starts, "+
				"and the environments you configured are simply not there.")
	}

	// warn rather than fail, and the distinction is the report's whole
	// vocabulary: fail here means a gate is off — an unrestricted shell, a
	// credential in the clear. With rta-full every gate still works. What
	// changes is how much sits behind none of them.
	if isOfficialFull(image) {
		r.add(grpAgentServers, name, stWarn,
			"points the agent at rta-full, where every bundled plugin is trusted at build time "+
				"and a read needs no grant — so a dozen plugins' read capabilities are reachable "+
				"with no consent step, and the tool list an injected prompt can choose from is "+
				"correspondingly larger. Every gate still applies; what widens is how much sits "+
				"behind none of them", refExcessivePriv)
		r.addFix("container-image", name+" — narrow the image to the plugins this job needs",
			"rta-full exists for a person at a terminal who wants a console. For an agent, use "+
				"`ghcr.io/this-is-tobi/rta` in "+shortPath(f.path)+", or build your own image "+
				"carrying only the plugins this job needs — see the \"share the image\" recipe in "+
				"docs/30-boundary/20-mcp.md. A plugin that is not in the image is one the agent "+
				"cannot reach at all, which is a cheaper boundary than any number of grants.")
	}

	if dir := env["RTA_DATA_DIR"]; dir != "" && !mounted(runtimeArgs, dir) {
		r.add(grpAgentServers, name, stWarn,
			"keeps its state at "+dir+" with nothing mounted there, so grants and the record are "+
				"lost with the container — every restart is a machine with no memory of what you "+
				"allowed", refMisconfig)
		r.addFix("container-state", name+" — let its grants and record outlive the container",
			"Mount a named volume at "+dir+" in "+shortPath(f.path)+":\n\n"+
				"  \"-v\", \"rta-home:"+dir+"\",\n\n"+
				"Without it the audit trail resets on every restart, and so does every grant you "+
				"issued — which reads as consent being re-asked rather than as state being lost.")
	}

	var absent []string
	if !hasBoolFlag(runtimeArgs, "--read-only") {
		absent = append(absent, "--read-only")
	}
	if !hasValue(runtimeArgs, "--cap-drop", "all") {
		absent = append(absent, "--cap-drop ALL")
	}
	if !hasValue(runtimeArgs, "--security-opt", "no-new-privileges") {
		absent = append(absent, "--security-opt no-new-privileges")
	}
	if len(absent) > 0 {
		r.add(grpAgentServers, name, stWarn,
			"containerised without "+strings.Join(absent, ", ")+" — the documented recipe drops "+
				"all of it because the server needs none of it", refExcessivePriv)
		r.addFix("container-hardening", name+" — apply the hardening the recipe specifies",
			"Add to the `docker run` arguments in "+shortPath(f.path)+":\n\n"+
				"  \"--read-only\", \"--cap-drop\", \"ALL\", \"--security-opt\", \"no-new-privileges\",\n\n"+
				"rta writes nothing outside its data volume, needs no capability, and gains "+
				"nothing from a setuid binary — so none of this costs it anything, which is why "+
				"the recipe hands over none of it.")
	}

	if len(flagValues(rtaArgs, "--root")) == 0 {
		r.add(grpAgentServers, name, stWarn,
			"runs `mcp serve` with no --root, and the path root defaults to the working "+
				"directory — which in a container is / unless the declaration says otherwise",
			refExcessivePriv)
		r.addFix("container-root", name+" — bound the paths it will accept",
			"Pair the container's working directory with rta's path root in "+shortPath(f.path)+":\n\n"+
				"  \"-w\", \"/work\", … \"mcp\", \"serve\", \"--root\", \"/work\"\n\n"+
				"Every path argument must sit under a root, so this is what stops a path argument "+
				"naming anything the container can see.")
	}

	// Reported when present, silent when absent — the opposite way round from
	// every other check here, and deliberately so.
	//
	// `--network none` is a bonus, not a baseline. It turns off every
	// capability that reaches the network — `audit web`, `net dns`, every
	// plugin that dials a remote database — so most people running rta for
	// what rta is for cannot use it, and the recipe's own table says to drop it
	// when you want those. A row for its absence would therefore fire on the
	// ordinary correct setup and read as a deficiency, which is how a report
	// trains people to skim past it. Saying so when somebody did close the
	// network costs nothing and is worth confirming.
	if hasValue(runtimeArgs, "--network", "none") {
		r.add(grpAgentServers, name, stOK,
			"containerised with the network closed — the strongest setting, and it means every "+
				"capability that reaches the network is off for this server", refExcessivePriv)
	}
}
