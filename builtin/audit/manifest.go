package audit

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/format"
	"github.com/this-is-tobi/rta/pkg/view"
)

// Reading what a project already declares, rather than resolving it.
//
// The distinction is the whole reason this stays inside the plugin's first
// rule. syft builds an SBOM by understanding build systems; trivy and grype
// walk images and resolve trees. None of that happens here: a lockfile is a
// list somebody's package manager already committed, an SBOM is a list
// somebody's build already produced, and reading a list is not scanning.
//
// Parsers are deliberately shallow. Each one extracts a name, a version and
// an ecosystem, and ignores everything else in the file. A shallow parser
// that skips what it does not recognise degrades into missing a component;
// a deep one that models a format it does not fully understand degrades into
// reporting the wrong version, which is worse in a security report.

// component is one dependency, in the spelling OSV uses.
type component struct {
	ecosystem string // OSV's exact, case-sensitive name: "Go", "npm", "PyPI", ...
	name      string
	version   string
	source    string // the file it was read from, so a finding can be acted on
}

func (c component) key() string { return c.ecosystem + "/" + c.name + "@" + c.version }

// manifestNames are the files worth looking for, in the order they are
// reported. SBOMs come first: when a project ships one, it is the more
// complete answer, and it is what the build actually produced.
var manifestNames = []string{
	// An SBOM is what the build actually produced, so it outranks the
	// lockfile it was generated from.
	"bom.json", "sbom.json", "cyclonedx.json", "sbom.spdx.json", "sbom.cdx.json",
	"go.mod",
	// Four package managers, four lockfiles, and a repository that has
	// switched carries more than one. dedupe collapses the overlap.
	"package-lock.json", "pnpm-lock.yaml", "yarn.lock", "bun.lock", "bun.lockb",
	// uv is displacing pip and Poetry fast enough that reading only
	// requirements.txt now misses whole projects.
	"uv.lock", "poetry.lock", "Pipfile.lock", "requirements.txt",
	"Cargo.lock", "composer.lock", "Gemfile.lock",
}

// ecosystems groups the manifest names for the error a caller sees when
// nothing was found. Sixteen filenames on one line is a wall; what the reader
// needs is whether their package manager is covered at all.
var ecosystems = []string{
	"Go (go.mod)",
	"npm/pnpm/yarn/bun (package-lock.json, pnpm-lock.yaml, yarn.lock, bun.lock)",
	"Python (uv.lock, poetry.lock, Pipfile.lock, requirements.txt)",
	"Rust (Cargo.lock)", "PHP (composer.lock)", "Ruby (Gemfile.lock)",
	"CycloneDX/SPDX SBOM (bom.json, sbom.json, ...)",
}

// skipDirs never hold a project's own declared dependencies, and all of them
// hold something that looks like one. node_modules is the case that decides
// the rule: a recursive scan that descends into it reports a project's
// transitive tree as if each copy were a separate project, and takes minutes
// to do it.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "bower_components": true,
	"target": true, "dist": true, "build": true, "out": true,
	"__pycache__": true, "venv": true, "site-packages": true, "Pods": true,
}

// maxScanDepth and maxManifests bound a recursive scan. The depth covers the
// monorepo layouts people actually use (apps/web, services/api/v2) without
// walking a whole home directory when --recursive meets a mistyped path; the
// count stops a pathological tree from turning one OSV query into fifty.
const (
	maxScanDepth = 6
	maxManifests = 200
)

// coverage is what a scan could not cover, carried back to the caller so
// the report can say so beside the findings. Every field is a caveat on the
// same claim — "this is what the project declares" — and a report that
// states it without them is stating it about part of a tree as though it
// were the whole.
type coverage struct {
	truncated  bool     // maxManifests or maxScanDepth stopped the walk
	unreadable []string // directories the walk could not list
	// withheld is the manifests the call's bounds refused: a file of rta's
	// own state or configuration under a root, by another name for it. Taken
	// for "not there" like any other failed stat, a directory holding only
	// that was answered "no lockfile or SBOM", which is not why nothing was
	// read.
	withheld []withheldManifest
}

// withheldManifest is a manifest the bounds refused, by its fs path, and
// their refusal of it.
type withheldManifest struct {
	name string
	err  *view.Error
}

// withheldNames is the manifests the bounds refused, as a report lists them.
func (c coverage) withheldNames() string {
	names := make([]string, len(c.withheld))
	for i, w := range c.withheld {
		names[i] = w.name
	}
	return strings.Join(names, ", ")
}

// addCoverage turns those caveats into findings. Shared so `audit deps` and
// `audit why` cannot come to describe the same shortfall differently.
func addCoverage(r *findings.Report, cov coverage) {
	if cov.truncated {
		r.Add(grpInventory, "scan", findings.Warn,
			"stopped at "+strconv.Itoa(maxManifests)+" manifests or "+strconv.Itoa(maxScanDepth)+
				" directory levels, so this covers part of the tree — narrow the path to audit the rest",
			refVulnerableDep)
	}
	if len(cov.withheld) > 0 {
		r.Add(grpInventory, "scan", findings.Warn,
			"withheld as another name for rta's own state or configuration, and missing from this audit: "+
				cov.withheldNames(),
			refVulnerableDep)
	}
	if len(cov.unreadable) > 0 {
		shown := cov.unreadable
		more := ""
		if len(shown) > 5 {
			more = fmt.Sprintf(" and %d more", len(shown)-5)
			shown = shown[:5]
		}
		r.Add(grpInventory, "scan", findings.Warn,
			format.CountOf(len(cov.unreadable), "directory")+" could not be read, so anything declared "+
				"inside is missing from this audit: "+strings.Join(shown, ", ")+more,
			refVulnerableDep)
	}
}

// findManifests looks in one directory, or accepts a file directly.
//
// It does not walk the tree by default, and that stays the default: "which
// directory" is a question the caller answers better than a heuristic, and a
// scan that wanders into node_modules reports a project's transitive tree as
// if each vendored copy were its own project.
//
// recursive is for the layout the default cannot serve: a monorepo. Whether
// its packages are declared workspaces or merely directories that happen to
// sit together turns out not to matter — a workspace-aware walk and a bounded
// directory walk find the same manifests, and only one of them needs a parser
// for four different workspace-declaration formats. For JavaScript the root
// lockfile usually already covers every workspace, since all four package
// managers hoist; the case that genuinely needs this is the polyglot repo,
// where the Go service, the Python worker and the web app each declare their
// own and no single file knows about the others.
//
// The bounds are reported rather than applied silently — see truncated.
// It reads through an fs.FS rather than through os, which is what lets the
// same scanner answer for a directory on this machine and for a repository
// nobody checked out — os.DirFS on one side, the clone's own filesystem on
// the other. Everything below therefore speaks slash-separated fs paths;
// what the *reader* is shown is a separate string, because the path inside a
// clone means nothing to them and the URL means nothing to fs.FS.
func findManifests(fsys fs.FS, recursive bool) (found []string, cov coverage, err error) {
	if !recursive {
		found, withheld := manifestsIn(fsys, ".")
		return found, coverage{withheld: withheld}, nil
	}
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is not a reason to abandon the
			// eleven that were readable — but it is a reason to say so.
			// Skipping it silently made a scan that covered part of a
			// monorepo indistinguishable from one that covered all of it,
			// and a dependency audit's whole claim is what it looked at.
			if d != nil && d.IsDir() {
				cov.unreadable = append(cov.unreadable, p)
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p != "." && (skipDirs[name] || strings.HasPrefix(name, ".")) {
			return fs.SkipDir
		}
		if depthOf(p) > maxScanDepth {
			return fs.SkipDir
		}
		if len(found) >= maxManifests {
			cov.truncated = true
			return fs.SkipAll
		}
		here, withheld := manifestsIn(fsys, p)
		found = append(found, here...)
		cov.withheld = append(cov.withheld, withheld...)
		return nil
	})
	if len(found) > maxManifests {
		found, cov.truncated = found[:maxManifests], true
	}
	return found, cov, err
}

// manifestsIn lists the manifests directly in one directory, in the order
// manifestNames declares, and those the call's bounds refused. A refusal
// from the bounds is the one *view.Error a stat here gives: the filesystem
// on this machine and a clone's answer with the platform's own errors.
func manifestsIn(fsys fs.FS, dir string) (found []string, withheld []withheldManifest) {
	for _, name := range manifestNames {
		full := path.Join(dir, name)
		st, err := fs.Stat(fsys, full)
		var refused *view.Error
		switch {
		case err == nil && !st.IsDir():
			found = append(found, full)
		case errors.As(err, &refused):
			withheld = append(withheld, withheldManifest{name: full, err: refused})
		}
	}
	return found, withheld
}

// depthOf counts directory levels below the root. fs paths are already
// relative to it, so this is a separator count and not a filepath.Rel.
func depthOf(p string) int {
	if p == "." {
		return 0
	}
	return strings.Count(p, "/") + 1
}

// parseManifest reads one file twice over: once for what it lists, once for
// what it says about the shape of that list.
//
// Two passes over the same bytes rather than one parser doing both, and the
// separation is the point rather than an accident of how it grew. The
// component parsers must not be wrong — a wrong version in a security report
// is an all-clear for something that is affected — while the graph parsers
// answer a question whose worst failure is "no explanation offered". Keeping
// them apart means no amount of care or carelessness in the second can reach
// the first. The cost is one extra scan of a file already in memory.
// name is where to read it; shown is what a finding calls it. They are the
// same string for a directory on this machine and necessarily different for
// a clone, and the parsers only ever see the second — a component's source
// is a thing somebody has to be able to go and open.
func parseManifest(fsys fs.FS, name, shown string) ([]component, graph, []string, error) {
	// Decided before the read: nothing here can parse a binary lockfile, so
	// pulling one into memory only makes the failure slower.
	if path.Base(name) == "bun.lockb" {
		return nil, graph{}, nil, errBinaryLockfile
	}
	data, err := readManifest(fsys, name, shown)
	if err != nil {
		return nil, graph{}, nil, err
	}
	// From shown, which is the path as the caller gave it: a file named on
	// its own is read under its base name alone, and requirements/prod.txt is
	// a requirements file only by the directory it sits in.
	format := manifestFormat(shown)
	comps, err := parseComponents(format, data, shown)
	if err != nil {
		return nil, graph{}, nil, err
	}
	var gaps []string
	if format == "requirements.txt" {
		gaps = requirementGaps(string(data))
	}
	return comps, parseGraph(format, data), gaps, nil
}

// readManifest reads name from fsys, refusing a file past maxManifestBytes by
// the name a finding calls it, shown. Through the filesystem's Open and not
// fs.ReadFile, so a directory on this machine opens it as pathin.FS does, and a
// clone's file is held to the same bound as one on disk.
func readManifest(fsys fs.FS, name, shown string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return pathin.ReadAll(f, shown, maxManifestBytes)
}

// manifestFormat names the format the manifest at p is read as: its own name
// for one of manifestNames, and for any JSON — an SBOM is known by what is
// inside it, not by what it is called — "requirements.txt" for a pip
// requirements file by any of the names projects give them, and "" for a
// file this does not read.
//
// A directory scan only ever finds the names in manifestNames. A file named
// on its own can be called anything, and requirements-dev.txt holding two
// pins was dispatched on its exact name, matched nothing, and read as a file
// with no pinned dependencies — the report blaming ranges it did not have.
func manifestFormat(p string) string {
	base := filepath.Base(p)
	switch {
	case slices.Contains(manifestNames, base), strings.HasSuffix(base, ".json"):
		return base
	case isRequirements(p):
		return "requirements.txt"
	}
	return ""
}

// isRequirements reports whether p is a pip requirements file under a name
// other than requirements.txt: requirements-dev.txt, dev-requirements.txt,
// or any .txt in a requirements/ directory.
func isRequirements(p string) bool {
	base := strings.ToLower(filepath.Base(p))
	if !strings.HasSuffix(base, ".txt") {
		return false
	}
	return strings.HasPrefix(base, "requirements") || strings.HasSuffix(base, "requirements.txt") ||
		filepath.Base(filepath.Dir(p)) == "requirements"
}

// parseComponents dispatches on the file's format (manifestFormat) and, for
// JSON, on what is actually inside it — an SBOM's filename is a convention,
// its format is not.
func parseComponents(base string, data []byte, path string) ([]component, error) {
	switch base {
	case "go.mod":
		return parseGoMod(string(data), path), nil
	case "package-lock.json":
		return parsePackageLock(data, path)
	case "pnpm-lock.yaml":
		return parsePnpmLock(string(data), path), nil
	case "yarn.lock":
		return parseYarnLock(string(data), path), nil
	case "bun.lock":
		return parseBunLock(data, path)
	case "uv.lock":
		return parseTOMLLock(string(data), path, "PyPI", false), nil
	case "poetry.lock":
		return parseTOMLLock(string(data), path, "PyPI", false), nil
	case "Cargo.lock":
		// A Cargo workspace member has no source at all, which is how it is
		// told apart from a crate that came from crates.io.
		return parseTOMLLock(string(data), path, "crates.io", true), nil
	case "composer.lock":
		return parseComposerLock(data, path)
	case "Pipfile.lock":
		return parsePipfileLock(data, path)
	case "Gemfile.lock":
		return parseGemfileLock(string(data), path), nil
	case "requirements.txt":
		return parseRequirements(string(data), path), nil
	}
	if strings.HasSuffix(base, ".json") {
		return parseSBOM(data, path)
	}
	return nil, nil
}

// parseGoMod reads the require directives, as the replace directives change
// them. Go modules keep their "v" prefix in OSV, so the version is used
// exactly as written.
//
// **A replace is what the build uses**, so it is what OSV is asked about.
// Read as the require alone, `require golang.org/x/net v0.38.0` beside
// `replace golang.org/x/net => golang.org/x/net v0.5.0` asked about the clean
// v0.38.0 while the build ran v0.5.0 — and the Kubernetes pattern of
// requiring k8s.io/api v0.0.0 and replacing it asked about a version that does
// not exist. See goModBuilds for which replace applies.
func parseGoMod(text, source string) []component {
	replaced := goModReplaces(text)
	var out []component
	goModLines(text, func(verb, line, _ string) {
		fields := strings.Fields(line)
		if verb != "require" || len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
			return
		}
		if t, ok := goModBuilds(replaced, fields[0], fields[1]); ok {
			out = append(out, component{ecosystem: "Go", name: t.name, version: t.version, source: source})
		}
	})
	return out
}

// goModLines calls fn with each directive in a go.mod: its verb, the rest of
// the line, and the comment after it. A line inside a `require (` block gets
// the block's verb, as the go command reads it.
//
// The one scanner both readers of go.mod share — parseGoMod for what it
// lists, goModGraph for the `// indirect` marker — so the two cannot disagree
// about which lines are a require, and a replace read by one is the replace
// read by the other.
func goModLines(text string, fn func(verb, line, comment string)) {
	block := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		comment := ""
		if i := strings.Index(line, "//"); i >= 0 {
			comment, line = line[i:], strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		if block != "" {
			if line == ")" {
				block = ""
				continue
			}
			fn(block, line, comment)
			continue
		}
		// `require (` as gofmt spells it, and `require(` as the go command
		// also accepts it.
		if opens, ok := strings.CutSuffix(line, "("); ok && !strings.ContainsAny(strings.TrimSpace(opens), " \t") {
			block = strings.TrimSpace(opens)
			continue
		}
		verb, rest, _ := strings.Cut(line, " ")
		fn(verb, strings.TrimSpace(rest), comment)
	}
}

// goModTarget is a module at a version: what a replace directive puts in
// place of a require, with neither set for a directory on this machine.
type goModTarget struct{ name, version string }

// goModReplaces reads a go.mod's replace directives, keyed "module" or, for
// one that names a version on its left, "module@version".
func goModReplaces(text string) map[string]goModTarget {
	replaced := map[string]goModTarget{}
	goModLines(text, func(verb, line, _ string) {
		old, repl, ok := strings.Cut(line, "=>")
		from, to := strings.Fields(old), strings.Fields(repl)
		if verb != "replace" || !ok || len(from) == 0 || len(from) > 2 || len(to) == 0 || len(to) > 2 {
			return
		}
		key := from[0]
		if len(from) == 2 {
			key += "@" + from[1]
		}
		var t goModTarget // a directory: no module, no version
		if len(to) == 2 {
			t = goModTarget{name: to[0], version: to[1]}
		}
		replaced[key] = t
	})
	return replaced
}

// goModBuilds is what the build uses for `require name version`: the require
// itself, or what a replace puts in its place — and false for a module
// replaced by a directory, which is on no registry and is left out, as a path
// dependency is in every other format read here. A replace naming a version
// on its left applies to that version only, and wins over one that does not,
// as it does for the go command.
//
// Known limit: a module replaced by a fork is reported under the fork's path,
// which is what OSV indexes it by, while `go mod why -m` — the command the
// provenance row names — wants the path it was required by. A fork carrying
// an advisory of its own is rare enough that the row is left to say the
// wrong path rather than carry a second name through every component.
func goModBuilds(replaced map[string]goModTarget, name, version string) (goModTarget, bool) {
	t, ok := replaced[name+"@"+version]
	if !ok {
		t, ok = replaced[name]
	}
	switch {
	case !ok:
		return goModTarget{name: name, version: version}, true
	case t.version == "":
		return goModTarget{}, false
	}
	return t, true
}

// npmLock covers lockfile v2 and v3 (the "packages" map) and v1 (the nested
// "dependencies" tree). Both shapes appear in the wild and v1 is still what
// older projects carry.
type npmLock struct {
	Packages map[string]struct {
		// Name is written only where it differs from the path the package is
		// installed at, which is an alias: see npmAlias.
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"packages"`
	Dependencies map[string]npmV1Dependency `json:"dependencies"`
}

// npmV1Dependency is one entry of a v1 lockfile's tree, and the copies nested
// under it: a version that conflicts with the hoisted one is installed beside
// whatever needed it, and recorded there — often the older copy, and the one
// an advisory names.
type npmV1Dependency struct {
	Version      string                     `json:"version"`
	Dependencies map[string]npmV1Dependency `json:"dependencies"`
}

func parsePackageLock(data []byte, source string) ([]component, error) {
	var lock npmLock
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	var out []component
	for path, pkg := range lock.Packages {
		// The root project is the empty key, and it is not a dependency.
		// Nested paths ("node_modules/a/node_modules/b") name the package
		// after their last node_modules segment — or by the name the entry
		// gives, where an alias installed it under another.
		if path == "" || pkg.Version == "" {
			continue
		}
		name, installed := npmPackageName(path)
		if !installed || name == "" {
			continue
		}
		if pkg.Name != "" {
			name = pkg.Name
		}
		out = append(out, component{ecosystem: "npm", name: name, version: pkg.Version, source: source})
	}
	if len(out) == 0 {
		out = npmV1Components(lock.Dependencies, source)
	}
	return out, nil
}

// npmV1Components reads a v1 tree, every level of it: read at the top alone,
// a nested copy — lodash 4.17.4 under the package that needed it, beside a
// hoisted 4.17.21 — never reached OSV, although the npmLock comment said the
// nested tree was covered. A copy nested in several places is listed once.
//
// Walked with a stack rather than by recursion. encoding/json already bounds
// the nesting a file can reach, so this is not a guard; it keeps the walk's
// cost in the heap where the file's size already put it.
func npmV1Components(deps map[string]npmV1Dependency, source string) []component {
	var out []component
	seen := map[string]bool{}
	pending := []map[string]npmV1Dependency{deps}
	for len(pending) > 0 {
		level := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		for name, pkg := range level {
			if len(pkg.Dependencies) > 0 {
				pending = append(pending, pkg.Dependencies)
			}
			if name == "" || pkg.Version == "" {
				continue
			}
			// v1 writes an alias's version as the alias itself.
			version := pkg.Version
			if real, v, ok := npmAlias(version); ok {
				name, version = real, v
			}
			c := component{ecosystem: "npm", name: name, version: version, source: source}
			if !seen[c.key()] {
				seen[c.key()] = true
				out = append(out, c)
			}
		}
	}
	return out
}

// parseRequirements takes only the pinned lines. A range ("django>=4.2") does
// not name a version, and guessing which one is installed would put a version
// this file never stated into a security report.
func parseRequirements(text, source string) []component {
	var out []component
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "-") {
			continue // flags, -r includes, --hash lines
		}
		name, version, ok := strings.Cut(line, "==")
		if !ok {
			continue
		}
		// Strip extras ("celery[redis]") and any trailing environment marker.
		if i := strings.IndexAny(name, "[ \t"); i >= 0 {
			name = name[:i]
		}
		version, _, _ = strings.Cut(version, ";")
		// Fields, not Split, returns an empty slice for a whitespace-only
		// string, so `foo==` — or a CRLF file's `foo==\r`, or `foo==  # pin
		// later` — indexed [0] on nothing and panicked. The trailing space
		// this line used to append was there to prevent exactly that and
		// could not: it made the string whitespace-only rather than empty,
		// which is the one input Fields returns nothing for.
		fields := strings.Fields(version)
		if len(fields) == 0 {
			continue
		}
		version = fields[0]
		if name = strings.TrimSpace(name); name == "" || version == "" {
			continue
		}
		out = append(out, component{ecosystem: "PyPI", name: name, version: version, source: source})
	}
	return out
}

// requirementGaps is what a requirements file lists that parseRequirements
// does not check: a range ("django>=4.2"), a URL or a VCS reference, and the
// other files it includes (-r, -c), which are read only if something else
// finds them. Each is named: by its package where there is one, by the line's
// own words where there is not.
//
// **A pin that is not there was a line that was not mentioned.** parseRequirements
// takes only pinned lines, rightly, and `django>=4.2` on the line above
// `flask==2.0.0` was dropped without a word: seven dependencies declared, no
// sign that an eighth was never looked at. The same report says outright when
// a file has no pin at all; one with some had nothing to say.
func requirementGaps(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		// A comment starts at a # that begins the line or follows a space, as
		// pip reads one: the # of "#egg=name" on a URL is part of it.
		if strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexAny(line, " \t"); i >= 0 {
			if j := strings.Index(line[i:], "#"); j >= 0 {
				line = strings.TrimSpace(line[:i+j])
			}
		}
		switch {
		case line == "":
		case includeFlag(line):
			out = append(out, line)
		case strings.HasPrefix(line, "-"):
			// --hash, --index-url, -e . and the rest are options, not packages.
		case len(parseRequirements(line, "")) == 0:
			out = append(out, requirementName(line))
		}
	}
	return out
}

// includeFlag reports whether line pulls another requirements file in: -r and
// -c, attached to their file or not, and their long spellings.
func includeFlag(line string) bool {
	if strings.HasPrefix(line, "--") {
		long, _, _ := strings.Cut(strings.Fields(line)[0], "=")
		return long == "--requirement" || long == "--constraint"
	}
	return strings.HasPrefix(line, "-r") || strings.HasPrefix(line, "-c")
}

// requirementName is the package a requirement line names, or the line's own
// first words where it is a URL without one.
func requirementName(line string) string {
	if _, egg, ok := strings.Cut(line, "#egg="); ok && egg != "" {
		return egg
	}
	name := line
	if i := strings.IndexAny(name, "<>=!~;[ \t@"); i > 0 {
		name = name[:i]
	}
	return findings.Clip(name)
}

// sbom covers the two formats that matter, distinguished by the field each
// one uses to announce itself.
type sbom struct {
	sbomMarks
	Components []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		PURL    string `json:"purl"`
	} `json:"components"`
	Packages []struct {
		Name         string `json:"name"`
		VersionInfo  string `json:"versionInfo"`
		ExternalRefs []struct {
			ReferenceType    string `json:"referenceType"`
			ReferenceLocator string `json:"referenceLocator"`
		} `json:"externalRefs"`
	} `json:"packages"`
}

// sbomMarks are the fields each SBOM format announces itself by, and all
// that has to be read to tell one from any other JSON.
type sbomMarks struct {
	BOMFormat   string `json:"bomFormat"`   // CycloneDX
	SPDXVersion string `json:"spdxVersion"` // SPDX
}

func (m sbomMarks) isSBOM() bool { return m.BOMFormat != "" || m.SPDXVersion != "" }

func parseSBOM(data []byte, source string) ([]component, error) {
	var b sbom
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if !b.isSBOM() {
		return nil, nil // some other JSON file that happens to sit here
	}
	var out []component
	for _, c := range b.Components {
		if got, ok := fromPURL(c.PURL, source); ok {
			out = append(out, got)
		} else if c.Name != "" && c.Version != "" {
			out = append(out, component{name: c.Name, version: c.Version, source: source})
		}
	}
	for _, p := range b.Packages {
		var added bool
		for _, ref := range p.ExternalRefs {
			if !strings.EqualFold(ref.ReferenceType, "purl") {
				continue
			}
			if got, ok := fromPURL(ref.ReferenceLocator, source); ok {
				out = append(out, got)
				added = true
				break
			}
		}
		if !added && p.Name != "" && p.VersionInfo != "" {
			out = append(out, component{name: p.Name, version: p.VersionInfo, source: source})
		}
	}
	return out, nil
}

// purlEcosystems maps package-URL types to OSV's ecosystem names, which are
// exact and case-sensitive — a wrong case returns no vulnerabilities rather
// than an error, which is the worst possible failure mode for this.
var purlEcosystems = map[string]string{
	"golang":   "Go",
	"npm":      "npm",
	"pypi":     "PyPI",
	"cargo":    "crates.io",
	"maven":    "Maven",
	"gem":      "RubyGems",
	"nuget":    "NuGet",
	"composer": "Packagist",
	"hex":      "Hex",
	"pub":      "Pub",
	"conan":    "ConanCenter",
	"cran":     "CRAN",
	"swift":    "SwiftURL",
}

// fromPURL reads "pkg:type/namespace/name@version". A component whose type is
// not one OSV knows is returned as unrecognised rather than guessed at, and
// the caller reports how many of those there were.
func fromPURL(purl, source string) (component, bool) {
	if !strings.HasPrefix(purl, "pkg:") {
		return component{}, false
	}
	rest := strings.TrimPrefix(purl, "pkg:")
	rest, _, _ = strings.Cut(rest, "?") // qualifiers
	rest, _, _ = strings.Cut(rest, "#") // subpath
	typ, rest, ok := strings.Cut(rest, "/")
	if !ok {
		return component{}, false
	}
	// The LAST '@' in the string, not the first: a scoped npm package's own
	// leading '@' — written literally or, per the purl spec, percent-encoded
	// as %40 — must not be mistaken for the version separator. A version
	// never contains '@', so whichever one appears last always is the real
	// separator, whether or not the name before it has one of its own.
	i := strings.LastIndex(rest, "@")
	if i < 0 {
		return component{}, false
	}
	path, version := rest[:i], rest[i+1:]
	if version == "" || path == "" {
		return component{}, false
	}
	// The namespace/name portion of a purl is percent-encoded per spec —
	// %40 for npm's leading '@' is the common case a real SBOM generator
	// emits. Decoded here so the name this stores and later queries OSV
	// with is the actual package name, not its escaped spelling; a no-op
	// for any purl already written with the literal, unescaped form.
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded
	}
	eco, known := purlEcosystems[strings.ToLower(typ)]
	if !known {
		return component{name: path, version: version, source: source}, true
	}
	name := path
	// Maven identifies a package as group:artifact; every other ecosystem
	// with a namespace keeps the slash (npm scopes, golang module paths).
	if eco == "Maven" {
		if group, artifact, ok := strings.Cut(path, "/"); ok {
			name = group + ":" + artifact
		}
	}
	return component{ecosystem: eco, name: name, version: version, source: source}, true
}

// dedupe collapses the same package reported by more than one manifest — an
// SBOM sitting next to the lockfile it was generated from is the normal case,
// not an odd one.
func dedupe(in []component) []component {
	seen := make(map[string]bool, len(in))
	out := make([]component, 0, len(in))
	for _, c := range in {
		if c.name == "" || c.version == "" || seen[c.key()] {
			continue
		}
		seen[c.key()] = true
		out = append(out, c)
	}
	return out
}
