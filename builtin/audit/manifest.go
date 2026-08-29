package audit

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
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
func findManifests(path string, recursive bool) (found []string, truncated bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	if !info.IsDir() {
		return []string{path}, false, nil
	}
	if !recursive {
		return manifestsIn(path), false, nil
	}
	root := filepath.Clean(path)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subdirectory is not a reason to abandon the
			// eleven that were readable.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		name := d.Name()
		if p != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
			return fs.SkipDir
		}
		if depthOf(root, p) > maxScanDepth {
			return fs.SkipDir
		}
		if len(found) >= maxManifests {
			truncated = true
			return fs.SkipAll
		}
		found = append(found, manifestsIn(p)...)
		return nil
	})
	if len(found) > maxManifests {
		found, truncated = found[:maxManifests], true
	}
	return found, truncated, err
}

// manifestsIn lists the manifests directly in one directory, in the order
// manifestNames declares.
func manifestsIn(dir string) []string {
	var found []string
	for _, name := range manifestNames {
		full := filepath.Join(dir, name)
		if st, err := os.Stat(full); err == nil && !st.IsDir() {
			found = append(found, full)
		}
	}
	return found
}

func depthOf(root, p string) int {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
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
func parseManifest(path string) ([]component, graph, error) {
	base := filepath.Base(path)
	// Decided before the read: nothing here can parse a binary lockfile, so
	// pulling one into memory only makes the failure slower.
	if base == "bun.lockb" {
		return nil, graph{}, errBinaryLockfile
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, graph{}, err
	}
	comps, err := parseComponents(base, data, path)
	if err != nil {
		return nil, graph{}, err
	}
	return comps, parseGraph(base, data), nil
}

// parseComponents dispatches on the file's name and, for JSON, on what is
// actually inside it — an SBOM's filename is a convention, its format is not.
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

// parseGoMod reads the require directives. Go modules keep their "v" prefix
// in OSV, so the version is used exactly as written.
func parseGoMod(text, source string) []component {
	var out []component
	inBlock := false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i]) // drop "// indirect" and friends
		}
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case strings.HasPrefix(line, "require "):
			line = strings.TrimSpace(strings.TrimPrefix(line, "require "))
		case !inBlock:
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[1], "v") {
			continue
		}
		out = append(out, component{ecosystem: "Go", name: fields[0], version: fields[1], source: source})
	}
	return out
}

// npmLock covers lockfile v2 and v3 (the "packages" map) and v1 (the nested
// "dependencies" tree). Both shapes appear in the wild and v1 is still what
// older projects carry.
type npmLock struct {
	Packages map[string]struct {
		Version string `json:"version"`
	} `json:"packages"`
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
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
		// after their last node_modules segment.
		if path == "" || pkg.Version == "" {
			continue
		}
		i := strings.LastIndex(path, "node_modules/")
		if i < 0 {
			continue
		}
		name := path[i+len("node_modules/"):]
		if name == "" {
			continue
		}
		out = append(out, component{ecosystem: "npm", name: name, version: pkg.Version, source: source})
	}
	if len(out) == 0 {
		for name, pkg := range lock.Dependencies {
			if name == "" || pkg.Version == "" {
				continue
			}
			out = append(out, component{ecosystem: "npm", name: name, version: pkg.Version, source: source})
		}
	}
	return out, nil
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
		version = strings.TrimSpace(strings.Fields(version + " ")[0])
		if name = strings.TrimSpace(name); name == "" || version == "" {
			continue
		}
		out = append(out, component{ecosystem: "PyPI", name: name, version: version, source: source})
	}
	return out
}

// sbom covers the two formats that matter, distinguished by the field each
// one uses to announce itself.
type sbom struct {
	BOMFormat   string `json:"bomFormat"`   // CycloneDX
	SPDXVersion string `json:"spdxVersion"` // SPDX
	Components  []struct {
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

func parseSBOM(data []byte, source string) ([]component, error) {
	var b sbom
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if b.BOMFormat == "" && b.SPDXVersion == "" {
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
	path, version, ok := strings.Cut(rest, "@")
	if !ok || version == "" || path == "" {
		return component{}, false
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
