package audit

import (
	"encoding/json"
	"strings"
)

// The JavaScript lockfiles: npm's package-lock, pnpm-lock, yarn.lock and
// bun.lock each record the dependency graph their installer resolved, in
// four different shapes that answer the same question.

// npmLockGraph reads lockfile v2/v3, whose `packages` map carries both halves:
// the root entry names what the project asked for, and every other entry
// names what it in turn requires.
//
// v1's nested `dependencies` tree is deliberately not read. Its `requires`
// field holds ranges against an implicit hoisting layout, so reconstructing
// the edges means reproducing npm's resolver — which is the deep-parser trap,
// for a lockfile format npm stopped writing in 2020.
type npmLockGraphFile struct {
	Packages map[string]struct {
		// Name is written only for a copy an alias installed; see npmAlias.
		Name                 string                     `json:"name"`
		Version              string                     `json:"version"`
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
		PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
		Link                 bool                       `json:"link"`
	} `json:"packages"`
}

func npmLockGraph(data []byte) graph {
	var lock npmLockGraphFile
	if err := json.Unmarshal(data, &lock); err != nil {
		return graph{}
	}
	if len(lock.Packages) == 0 {
		return graph{} // v1, or something that is not a package lock at all
	}
	g := newGraph()
	var all []string
	// What the root and each workspace asked for, recorded **against the
	// manifest that asked** — and, second pass, the copy each of those
	// declarations actually resolved to.
	//
	// Attribution rather than a flat set of names, because npm hoists exactly
	// one copy to the top and leaves the rest nested beside whoever asked for
	// them. `node_modules/<name>` is therefore whichever declaration won that
	// race, which need not be the root's and need not be the one being looked
	// at. Verified against a lockfile npm wrote: two workspaces asking for
	// `commander` at `^7.2.0` and `^11.0.0` put 7.2.0 at `node_modules/commander`
	// and 11.1.0 at `packages/toolkit/node_modules/commander`.
	type declaration struct{ from, name string }
	var declared []declaration
	// Every installed copy by its path — the package installed there, and
	// its version — for the resolution walks below.
	//
	// The package, not the path's last segment: an alias installs lodash at
	// node_modules/old-lodash, and every declaration and edge naming
	// old-lodash means that copy of lodash. Named by the path, the graph held
	// a package the inventory — which reads the entry's name — never lists,
	// and `audit why lodash` found no route to the copy it reported.
	packageAt := map[string]string{}
	installedAt := map[string]string{}
	aliased := map[string]bool{} // a name some copy is installed under that is not its own
	for path, pkg := range lock.Packages {
		name, installed := npmPackageName(path)
		// A `link: true` entry is a symlink into a workspace, not an installed
		// copy: npm writes `node_modules/@acme/app` pointing at `packages/app`.
		// Counted as installed it landed in `all`, so stateIndirect marked the
		// project's own workspace package "indirect".
		if pkg.Link || !installed {
			continue
		}
		if pkg.Name != "" && pkg.Name != name {
			aliased[name] = true
			name = pkg.Name
		}
		packageAt[path] = name
		all = append(all, ref("npm", name))
		// Only a real version. An entry that records none — npm writes one
		// for a link, a bundled dependency, or a resolution it could not pin
		// — would otherwise resolve *successfully* to the empty string, and
		// pin() then marks the name pinned while leaving directAt empty, so
		// relation takes the pinned branch, matches nothing, and answers
		// "indirect" for a package the root package.json names. Every sibling
		// parser already refuses an empty version here.
		if pkg.Version != "" {
			installedAt[path] = pkg.Version
		}
	}
	walk := npmWalk{left: npmWalkBudget}
	// The package a dependency called dep, declared at from, loads: the one
	// installed where Node finds it. Walked only for a name an alias uses —
	// every other name is the package it names, and a walk per edge is a
	// cost the budget is better kept for.
	packageFor := func(from, dep string) string {
		if !aliased[dep] {
			return dep
		}
		if at, ok := walk.resolve(packageAt, from, dep); ok {
			return packageAt[at]
		}
		return dep
	}
	for path, pkg := range lock.Packages {
		if pkg.Link {
			continue
		}
		// The root entry ("") and each workspace entry (a path with no
		// node_modules segment) name what somebody wrote in a package.json,
		// which is the definition of a direct dependency.
		if _, installed := npmPackageName(path); !installed {
			for _, set := range []map[string]json.RawMessage{
				pkg.Dependencies, pkg.DevDependencies, pkg.OptionalDependencies,
			} {
				for dep := range set {
					g.direct[ref("npm", packageFor(path, dep))] = true
					declared = append(declared, declaration{from: path, name: dep})
				}
			}
			continue
		}
		name, ok := packageAt[path]
		if !ok {
			continue
		}
		// devDependencies of an installed package are not installed, so they
		// are not why anything is in this tree. peerDependencies are, when
		// they resolve — and an unresolved peer simply has no node of its own
		// for the chain to reach.
		for _, set := range []map[string]json.RawMessage{
			pkg.Dependencies, pkg.OptionalDependencies, pkg.PeerDependencies,
		} {
			for dep := range set {
				g.require(ref("npm", name), ref("npm", packageFor(path, dep)))
			}
		}
	}
	a := newAsked()
	for _, d := range declared {
		r := ref("npm", packageFor(d.from, d.name))
		if at, ok := walk.resolve(installedAt, d.from, d.name); ok {
			a.found(r, installedAt[at])
		} else {
			a.missing(r)
		}
	}
	a.apply(&g)
	g.stateIndirect(all)
	// A declaration the budget left unresolved has no pin, so relation falls
	// back to what the name alone says — and the report says the structure
	// was not read whole, rather than let that read like a stated answer.
	g.truncated = g.truncated || walk.cut
	return g
}

// npmWalk resolves declarations by Node's own lookup, within a budget.
//
// **The budget is the file's shape, not its size.** Each step up builds the
// path of one more ancestor to look in, so a declaration costs its manifest's
// depth times its path's length — nothing for a real workspace, "packages/app"
// three steps up, and cubic in the file for one somebody wrote to be walked:
// a workspace entry thousands of directories deep that declares thousands of
// packages. Measured at 140 ms for 13 KB and doubling the file multiplying
// it by six, so a lockfile of a few hundred kilobytes, on a checkout audit.deps
// reads over MCP with no grant, held the call for minutes. A real lockfile
// builds well under a megabyte of these paths; one that spends the budget
// gets the rest of its declarations unresolved and the walk marked cut.
type npmWalk struct {
	left int  // bytes of candidate paths still to be built
	cut  bool // a resolution was abandoned for the budget
}

// npmWalkBudget is what one lockfile's resolutions may build between them.
const npmWalkBudget = 32 << 20

// resolve finds the copy one manifest's declaration resolved to, by walking
// Node's own lookup: the nearest node_modules holding the name, starting beside
// the manifest that declared it and rising to the root. `from` is the
// manifest's own key — "" for the root, "packages/app" for a workspace, an
// installed copy's path for what it requires. It returns the copy's key in
// installed.
func (w *npmWalk) resolve(installed map[string]string, from, name string) (string, bool) {
	for dir := from; ; {
		// Counted before it is built, so the step that would overspend costs
		// nothing.
		cost := len(dir) + len("/node_modules/") + len(name)
		if cost > w.left {
			w.left, w.cut = 0, true
			return "", false
		}
		w.left -= cost
		key := "node_modules/" + name
		if dir != "" {
			key = dir + "/" + key
		}
		if _, ok := installed[key]; ok {
			return key, true
		}
		if dir == "" {
			return "", false
		}
		if i := strings.LastIndexByte(dir, '/'); i >= 0 {
			dir = dir[:i]
		} else {
			dir = ""
		}
	}
}

// npmPackageName reads a `packages` key. The bool says whether the key names
// something installed into a node_modules tree, as opposed to the project's
// own root or one of its workspaces.
func npmPackageName(path string) (string, bool) {
	i := strings.LastIndex(path, "node_modules/")
	if i < 0 {
		return path, false
	}
	return path[i+len("node_modules/"):], true
}

// pnpmGraph reads the blocks pnpm splits the answer across: `importers:` says
// what each workspace asked for, and `snapshots:` (v9) or `packages:` (v5/v6)
// says what each package pulled in.
//
// Indentation is the grammar, as it is for every pnpm version: the block at
// column zero, its entries at two, their `dependencies:` at four, and the
// dependencies themselves at six.
//
// **Except in a v5/v6 lockfile for a project that is not a monorepo**, which
// omits `importers:` entirely and writes the root's own `dependencies:` at
// column zero instead — one indentation level up from everything above.
// Reading only `importers:` meant every package in such a file came back
// "the format did not say" when the format said it on line three: measured on
// a real vitepress project, 88 components and not one relation among them.
// It is the single most common pnpm layout there is, since most projects are
// one project.
func pnpmGraph(text string) graph {
	g := newGraph()
	const (
		none = iota
		importers
		packages
		rootDeps
	)
	block, entry, inDeps := none, "", false
	importing := ""
	var all []string
	// resolved pins the dependency being read to the version val says
	// satisfied it. Under an alias val names the package as well —
	// `string-width@4.2.3` for a string-width-cjs — and that package is the
	// one the project asked for, so it takes the mark and the pin. The alias
	// keeps its mark too: nothing is installed under it, so it relates
	// nothing.
	resolved := func(val string) {
		if c, ok := pnpmAlias(val); ok {
			importing = ref("npm", c.name)
			g.direct[importing] = true
			g.pin(importing, c.version)
			return
		}
		if v := pnpmVersion(val); v != "" {
			g.pin(importing, v)
		}
	}

	for _, raw := range strings.Split(text, "\n") {
		if strings.TrimSpace(raw) == "" || strings.HasPrefix(strings.TrimSpace(raw), "#") {
			continue
		}
		if raw[0] != ' ' && raw[0] != '\t' {
			switch strings.TrimSpace(raw) {
			case "importers:":
				block = importers
			case "packages:", "snapshots:":
				// v9 writes both: `packages:` holds resolutions and
				// `snapshots:` holds the edges. Reading both is harmless —
				// `packages:` simply has no dependencies block to find — and
				// it is what makes one scanner serve three lockfile versions.
				block = packages
			case "dependencies:", "devDependencies:", "optionalDependencies:":
				// The root importer of a single-project v5/v6 file. A monorepo
				// never reaches here: its equivalent lines live under
				// `importers:` at indent four.
				block = rootDeps
			default:
				block = none
			}
			entry, inDeps = "", false
			continue
		}
		if block == none {
			continue
		}
		switch indentOf(raw) {
		case 2:
			key := strings.TrimSuffix(strings.TrimSpace(raw), ":")
			inDeps, importing = false, ""
			if block == rootDeps {
				// v5 writes the resolved version inline (`vite: 4.0.0(x@1)`);
				// v6 leaves the value empty and puts `specifier:`/`version:`
				// at indent four. Both name the package here.
				name, val, _ := strings.Cut(strings.TrimSpace(raw), ":")
				name = strings.Trim(strings.TrimSpace(name), `'"`)
				if name == "" {
					continue
				}
				importing = ref("npm", name)
				g.direct[importing] = true
				resolved(val)
				continue
			}
			if block == importers {
				entry = key // a workspace path; its identity does not matter
				continue
			}
			if c, ok := pnpmComponent(key, ""); ok {
				entry = ref("npm", c.name)
				all = append(all, entry)
			} else {
				entry = ""
			}
		case 4:
			if block == rootDeps {
				// The v6 root importer's `version:`, the counterpart of the
				// indent-eight line below.
				if importing == "" {
					continue
				}
				key, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
				if ok && strings.TrimSpace(key) == "version" {
					resolved(val)
				}
				continue
			}
			// optionalDependencies are installed when they resolve, so they
			// explain a package's presence. peerDependencies under `packages:`
			// are a declaration about the consumer, not an install, and pnpm
			// records the resolved ones under snapshots anyway.
			switch strings.TrimSpace(raw) {
			case "dependencies:", "optionalDependencies:", "devDependencies:":
				inDeps = true
			default:
				inDeps = false
			}
		case 6:
			if !inDeps || entry == "" {
				continue
			}
			name, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
			if !ok {
				continue
			}
			name = strings.Trim(strings.TrimSpace(name), `'"`)
			if name == "" {
				continue
			}
			if block == importers {
				g.direct[ref("npm", name)] = true
				// Held so the `version:` line two below can pin it. v5 writes
				// the version here instead.
				importing = ref("npm", name)
				resolved(val)
			} else {
				if c, ok := pnpmAlias(val); ok {
					name = c.name
				}
				g.require(entry, ref("npm", name))
			}
		case 8:
			// An importer's dependency states which version satisfied it —
			// `version: 4.17.1(peer@1.0.0)` — which is the one thing that tells
			// the copy the project asked for from another copy of the same name
			// that something else dragged in.
			if block != importers || importing == "" {
				continue
			}
			key, val, ok := strings.Cut(strings.TrimSpace(raw), ":")
			if !ok || strings.TrimSpace(key) != "version" {
				continue
			}
			resolved(val)
		}
	}
	// Only a block that states the direct set makes it exhaustive. A lockfile
	// carrying packages and neither an importers block nor a root dependency
	// block is a fragment, and inferring "indirect" from it would be inferring
	// from silence.
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// pnpmVersion reads the resolved version off a pnpm `version:` value, dropping
// the peer-dependency suffix — `4.17.1(peer@1.0.0)` in v6 and v9, and
// `4.0.0_@types+node@20.11.0` in v5, both of which name the build rather than
// the version. Empty when there is nothing there, which is v6's own shape for
// a dependency whose version sits on the next line.
//
// Cutting at `_` is safe rather than a guess: semver permits `_` nowhere — not
// in the version core, not in a pre-release, and not in build metadata, which
// is alphanumerics and hyphens only. Left in, the suffix made the pin name a
// version no component could ever carry, which relation reads as "every copy
// of this package is somebody else's" — the same wrong answer the workspace
// fix above exists to remove.
func pnpmVersion(val string) string {
	v := strings.Trim(strings.TrimSpace(val), `'"`)
	if i := strings.IndexAny(v, "(_"); i > 0 {
		v = v[:i]
	}
	return v
}

// pnpmAlias reads the package a pnpm dependency's value names, which it does
// only for a copy an alias installed: `string-width@4.2.3` in v9,
// `/string-width@4.2.3` in v6, `/string-width/4.2.3` in v5, where any other
// dependency's value is its version alone — which starts with a digit.
func pnpmAlias(val string) (component, bool) {
	v := strings.Trim(strings.TrimSpace(val), `'"`)
	if v == "" || v[0] >= '0' && v[0] <= '9' {
		return component{}, false
	}
	return pnpmComponent(v, "")
}

// yarnGraph reads the `dependencies:` sub-block both yarn dialects write
// under an entry, and — for Berry, which records them — the workspace entries
// that say what the project itself asked for.
//
//	v1      "express@^4.17.1":        then    dependencies:\n    qs "6.7.0"
//	berry   "express@npm:^4.17.1":    then    dependencies:\n    qs: "npm:6.7.0"
func yarnGraph(text string) graph {
	g := newGraph()
	entry, workspace, inDeps := "", false, false
	var all []string
	// Berry states a workspace's dependency as the *spec* it asked for
	// (`express: "npm:^4.17.1"`), and the lockfile's own entry keys are those
	// specs — so `express@npm:^4.17.1` names the entry whose `version:` is the
	// copy the project got. Collected as the file is read and resolved at the
	// end, because an entry can appear before or after the workspace that
	// wants it.
	wantedSpec := map[string]string{} // "name@range" -> name
	specVersion := map[string]string{}
	current := []string(nil)

	for _, raw := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if raw[0] != ' ' && raw[0] != '\t' {
			entry, workspace, inDeps, current = "", false, false, nil
			if !strings.HasSuffix(trimmed, ":") {
				continue
			}
			specs := strings.Split(strings.TrimSuffix(trimmed, ":"), ",")
			for _, one := range specs {
				if one = strings.Trim(strings.TrimSpace(one), `'"`); one != "" {
					current = append(current, one)
				}
			}
			spec := ""
			if len(current) > 0 {
				spec = current[0]
			}
			if spec == "__metadata" {
				continue
			}
			at := specSeparator(spec)
			if at <= 0 {
				continue
			}
			name := spec[:at]
			if strings.HasPrefix(spec[at+1:], "workspace:") {
				// The project's own package. What it requires is what the
				// project asked for — the one place a yarn.lock states it.
				entry, workspace = name, true
				continue
			}
			if real, _, ok := npmAlias(spec[at+1:]); ok {
				name = real
			}
			entry = ref("npm", name)
			all = append(all, entry)
			continue
		}
		if entry == "" && !workspace {
			continue
		}
		switch indentOf(raw) {
		case 2:
			if key, val, ok := strings.Cut(trimmed, ":"); ok && strings.TrimSpace(key) == "version" {
				// Recorded against every spec this entry answers to, so a
				// workspace's declared range can be matched to what satisfied it.
				v := strings.Trim(strings.TrimSpace(val), `'"`)
				for _, one := range current {
					specVersion[one] = v
				}
			}
			switch strings.TrimPrefix(trimmed, "\"") {
			case "dependencies:", "optionalDependencies:", "dependencies\":":
				inDeps = true
			default:
				inDeps = false
			}
		case 4:
			if !inDeps {
				continue
			}
			// `qs "6.7.0"` (v1) or `qs: "npm:6.7.0"` (berry). The name is
			// everything before the first colon-or-space that is not part of a
			// scope, and a scope's @ is at position zero.
			name, value := trimmed, ""
			if i := strings.IndexAny(name[1:], ": "); i >= 0 {
				name, value = name[:i+1], name[i+1:]
			}
			name = strings.Trim(strings.TrimSuffix(name, ":"), `'"`)
			if name == "" {
				continue
			}
			// Under an alias, `string-width-cjs "npm:string-width@^4.2.0"`,
			// the package required is the one the value names.
			pkg := name
			if real, _, ok := npmAlias(strings.TrimPrefix(value, ":")); ok {
				pkg = real
			}
			if workspace {
				g.direct[ref("npm", pkg)] = true
				// The range this workspace asked for, verbatim, so the entry
				// that answers it can be found below — keyed as the entry is,
				// by the name asked for.
				if _, rng, ok := strings.Cut(trimmed, ":"); ok {
					rng = strings.Trim(strings.TrimSpace(rng), `'"`)
					if rng != "" {
						wantedSpec[name+"@"+rng] = pkg
					}
				}
			} else {
				g.require(entry, ref("npm", pkg))
			}
		}
	}
	for spec, name := range wantedSpec {
		if v, ok := specVersion[spec]; ok && v != "" {
			g.pin(ref("npm", name), v)
		}
	}
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// bunLockGraphFile is bun's text lockfile. A package entry is a positional
// array whose third element, when present, is the object holding its
// dependency maps.
type bunLockGraphFile struct {
	Workspaces map[string]struct {
		// Name is the workspace's own package name, which is how bun keys the
		// copies installed for it — see bunResolve.
		Name                 string                     `json:"name"`
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	} `json:"workspaces"`
	Packages map[string][]json.RawMessage `json:"packages"`
}

func bunGraph(data []byte) graph {
	var lock bunLockGraphFile
	if err := json.Unmarshal(stripJSONC(data), &lock); err != nil {
		return graph{}
	}
	g := newGraph()
	// Recorded against the workspace that asked, for the reason npmLockGraph
	// gives at length: one copy is hoisted and the rest are nested, so the
	// hoisted one is not necessarily the one any given workspace asked for.
	// name is the key the declaration is made under, which is the key bun
	// installs it under too; pkg is the package it names, which differs from
	// it under an alias.
	type declaration struct{ owner, name, pkg string }
	var declared []declaration
	for path, ws := range lock.Workspaces {
		// The root workspace's copies are the hoisted ones — bun writes them
		// under the bare name, never under the root package's name. Verified
		// against a lockfile bun wrote with the root and two workspaces all
		// declaring the same package.
		owner := ws.Name
		if path == "" {
			owner = ""
		}
		for _, set := range []map[string]json.RawMessage{
			ws.Dependencies, ws.DevDependencies, ws.OptionalDependencies,
		} {
			for dep, rng := range set {
				pkg := bunDependency(dep, rng)
				g.direct[ref("npm", pkg)] = true
				declared = append(declared, declaration{owner: owner, name: dep, pkg: pkg})
			}
		}
	}
	var all []string
	// Every resolved copy by its lockfile key, for bunResolve.
	installedAt := map[string]string{}
	for key, entry := range lock.Packages {
		if len(entry) == 0 {
			continue
		}
		var spec string
		if err := json.Unmarshal(entry[0], &spec); err != nil {
			continue
		}
		name, version, ok := bunSpec(spec)
		if !ok || !isVersionish(version) {
			continue // a workspace, not a package
		}
		from := ref("npm", name)
		all = append(all, from)
		installedAt[key] = version
		if len(entry) < 3 {
			continue
		}
		var meta struct {
			Dependencies         map[string]json.RawMessage `json:"dependencies"`
			OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
			PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
		}
		if err := json.Unmarshal(entry[2], &meta); err != nil {
			continue
		}
		for _, set := range []map[string]json.RawMessage{
			meta.Dependencies, meta.OptionalDependencies, meta.PeerDependencies,
		} {
			for dep, rng := range set {
				g.require(from, ref("npm", bunDependency(dep, rng)))
			}
		}
	}
	a := newAsked()
	for _, d := range declared {
		r := ref("npm", d.pkg)
		if version, ok := bunResolve(installedAt, d.owner, d.name); ok {
			a.found(r, version)
		} else {
			a.missing(r)
		}
	}
	a.apply(&g)
	if len(g.direct) > 0 {
		g.stateIndirect(all)
	}
	return g
}

// bunDependency is the package a bun.lock dependency map names under key
// dep: dep itself, or under an alias the package its range names —
// `"string-width-cjs": "npm:string-width@^4.2.0"`.
func bunDependency(dep string, rng json.RawMessage) string {
	var s string
	if json.Unmarshal(rng, &s) == nil {
		if real, _, ok := npmAlias(s); ok {
			return real
		}
	}
	return dep
}

// bunResolve finds the copy one workspace's declaration resolved to.
//
// bun keys a hoisted package by its bare name and every other copy by the
// *package name* of whatever holds it — "commander" against
// "@acme/toolkit/commander", not by the workspace's directory. So a
// workspace's own copy is looked for under its name first, and the hoisted one
// answers when it did not need a copy of its own.
func bunResolve(installedAt map[string]string, owner, name string) (string, bool) {
	if owner != "" {
		if version, ok := installedAt[owner+"/"+name]; ok {
			return version, true
		}
	}
	version, ok := installedAt[name]
	return version, ok
}
