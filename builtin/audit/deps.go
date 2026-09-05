package audit

import (
	"context"
	"errors"
	"io/fs"
	stdhttp "net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/gitclone"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// "Are we affected?" is the question that arrives at the worst possible
// moment — an advisory lands, and somebody has to answer it for every service
// they own before the meeting. The full answer needs a real scanner. The
// first ninety seconds of it needs this: read what the project already
// declares, ask OSV once, and say which dependencies are named in an advisory.
//
// A03:2025 Software Supply Chain Failures is a new category in the current
// OWASP edition, which is itself the argument for the capability: this moved
// up because it is where breaches are actually coming from.

// Groups order the detail page and name its sections.
var (
	grpVulnerable = group{"vulnerabilities", "known vulnerabilities"}
	grpInventory  = group{"inventory", "inventory"}
)

var depsGroupOrder = []group{grpVulnerable, grpInventory}

func runDeps(ctx context.Context, req plugin.Request) (view.View, error) {
	path := strings.TrimSpace(req.String("path"))
	if path == "" {
		path = "."
	}
	recursive := req.Bool("recursive")
	proj, verr := openProject(ctx, req, path)
	if verr != nil {
		return nil, verr
	}
	names, shown, truncated, err := proj.manifests(recursive)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, view.Errorf("audit.deps.nopath", "no such path: %s", path).
				WithHint("pass the directory holding the lockfile or SBOM, the file itself, " +
					"or a repository URL")
		}
		return nil, view.Errorf("audit.deps.path", "reading %s: %v", path, err)
	}
	if len(names) == 0 {
		hint := "reads what a project already declares, so one of these has to exist: " +
			strings.Join(ecosystems, "; ")
		if !recursive {
			hint += ". In a monorepo the manifests are a level down: try --recursive"
		}
		return nil, view.Errorf("audit.deps.nomanifest", "no lockfile or SBOM in %s", remoteLabel(path)).
			WithHint(hint)
	}

	inv := read(proj.fsys, names, shown)

	r := &report{}
	offline := req.Bool("offline")
	var (
		vulns   map[string][]string
		records map[string]osvRecord
		capped  bool
	)
	if !offline && len(inv.queryable) > 0 {
		timeout := time.Duration(req.Int("timeout")) * time.Second
		qctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		client := &stdhttp.Client{Timeout: timeout}
		vulns, err = queryOSV(qctx, client, inv.queryable)
		if err != nil {
			return nil, view.Errorf("audit.deps.osv", "querying osv.dev: %v", err).
				WithHint("use --offline to inventory the dependencies without asking anything")
		}
		// Only for what the first question found, which on a clean project is
		// nothing at all. The deadline is the one the caller already set for
		// the whole query, so grading cannot make an audit run longer than it
		// was asked to.
		if ids := everyAdvisory(vulns); len(ids) > 0 {
			records, capped = detailOSV(qctx, client, ids)
		}
	}

	gradeDeps(r, inv, vulns, records, capped, offline)
	if truncated {
		r.add(grpInventory, "scan", stWarn,
			"stopped at "+strconv.Itoa(maxManifests)+" manifests or "+strconv.Itoa(maxScanDepth)+
				" directory levels, so this covers part of the tree — narrow the path to audit the rest",
			refVulnerableDep)
	}
	if inv.truncated {
		r.add(grpInventory, "scan", stWarn,
			"one manifest declared more components than this reads at once, so the inventory covers "+
				"part of it — narrow the path to audit the rest",
			refVulnerableDep)
	}

	if req.Bool("detail") {
		summary := append([]view.Pair{
			{Key: "path", Value: remoteLabel(path)},
			{Key: "manifests", Value: manifestSummary(path, shown)},
			{Key: "dependencies", Value: strconv.Itoa(len(inv.all))},
		}, r.grade()...)
		summary = append(summary, depsDeeper(remoteLabel(path), gitclone.IsRemote(path), shown)...)
		return detailPage(ctx, req, r, depsGroupOrder, view.KeyValue{Pairs: summary}), nil
	}
	return r.table(true), nil
}

// How much of a chain is worth printing. Three explanations, eight links: a
// utility reached from forty places has not been explained by listing forty,
// and a chain of eleven names is a diagram rather than a sentence. Both
// bounds announce themselves — via() marks a chain it cut, and the "next
// step" finding names the command that prints the rest.
const (
	maxWhyChains = 3
	maxWhyDepth  = 8
)

// whereFrom is a vulnerable package's provenance, in the words that decide
// what somebody does next.
//
// "Direct" means bump a version in a file you own. "Indirect, pulled in by
// express" means the fix belongs to express, and the choices are wait,
// override the resolution, or stop using express. Those are different
// afternoons, and the report used to give no way to tell which one you were
// in.
//
// An empty string is a real answer and the one this returns most carefully:
// the manifest did not record the distinction and nothing requires this
// package in any file that was read. Saying nothing there is the point —
// see graph.go's first rule.
func whereFrom(inv inventory, c component) string {
	r := ref(c.ecosystem, c.name)
	chains := inv.structure.via(r, maxWhyChains, maxWhyDepth)
	// A tree holding two copies of one package makes every name-keyed
	// statement about it a statement about *both*, chains included: the file
	// records that express requires qs, not which qs. Said out loud rather
	// than left for the reader to assume otherwise, because "pulled in by
	// express" reads as a fact about the version in front of them.
	copies := ""
	if inv.ambiguous[r] {
		copies = "one of " + strconv.Itoa(inv.versionCount(r)) + " copies here"
	}
	switch inv.relation(c) {
	case relDirect:
		return "a direct dependency"
	case relIndirect:
		switch {
		case len(chains) == 0 && copies != "":
			return "indirect, " + copies
		case len(chains) == 0:
			return "indirect"
		case copies != "":
			return "indirect, " + copies + ", " + renderChains(chains)
		}
		return "indirect, " + renderChains(chains)
	}
	switch {
	case len(chains) > 0 && copies != "":
		return copies + ", " + renderChains(chains)
	case len(chains) > 0:
		return renderChains(chains)
	case copies != "":
		return copies
	}
	return ""
}

// versionCount is how many copies of a package the inventory holds.
func (inv inventory) versionCount(r string) int {
	seen := map[string]bool{}
	for _, c := range inv.all {
		if ref(c.ecosystem, c.name) == r {
			seen[c.version] = true
		}
	}
	return len(seen)
}

// renderChains reads the chains as a sentence, nearest cause first.
//
// The target is dropped from the end of each: the finding already names the
// package, and repeating it at the end of every chain is noise in the one
// cell that has the least room.
func renderChains(chains [][]string) string {
	parts := make([]string, 0, len(chains))
	for _, chain := range chains {
		names := make([]string, 0, len(chain)-1)
		for _, r := range chain[:len(chain)-1] {
			names = append(names, refName(r))
		}
		parts = append(parts, strings.Join(names, " → "))
	}
	return "pulled in by " + strings.Join(parts, "; also by ")
}

// gradeProvenance says what the manifests could not explain, and names the
// one command that can.
//
// This is where the plugin's first rule (it does not reimplement
// scanners) meets the question the operator actually has. Reading a
// committed lockfile is free and safe; resolving a module graph is a package
// manager's job, and every package manager already ships the command. So the
// report answers what the file states, and hands over the exact invocation —
// with the affected package already in it — for the rest.
func gradeProvenance(r *report, affected []component, inv inventory) {
	g := inv.structure
	if g.truncated {
		r.add(grpInventory, "structure", stWarn,
			"the dependency structure was larger than this reads, so a chain above may stop short of "+
				"the package that actually pulled it in", refVulnerableDep)
	}
	// The ones whose presence nothing in the tree explained: no chain leads to
	// them and the project did not ask for them by name.
	//
	// "Not stated" is not the test, and go.mod is why. It marks every require
	// direct or indirect and records no edges at all, so `// indirect` is a
	// complete statement of the relation and no statement whatsoever about
	// *what* pulled the module in — which is the half somebody needs to decide
	// whether the upgrade is theirs. A package with a relation and no chain is
	// still a package to hand over to `go mod why`.
	//
	// Ordered by the affected slice, which is already sorted, so the command
	// named is the same one twice running.
	var unexplained []component
	for _, c := range affected {
		if inv.relation(c) == relDirect {
			continue
		}
		if len(g.via(ref(c.ecosystem, c.name), 1, maxWhyDepth)) == 0 {
			unexplained = append(unexplained, c)
		}
	}
	if len(unexplained) == 0 {
		return
	}
	c := unexplained[0]
	subject := "it"
	if len(unexplained) > 1 {
		subject = "them"
	}
	detail := plural(len(unexplained), "affected package") + " could not be traced to what pulled " +
		subject + " in, because " + filepath.Base(c.source) + " does not record that"
	if cmd := whyCommand(c); cmd != "" {
		detail += " — `" + cmd + "` prints it"
	}
	r.add(grpInventory, "provenance", stInfo, detail, refVulnerableDep)
}

// whyCommand is the native invocation that answers "why is this here", with
// the package already substituted in so it can be copied rather than typed.
//
// Chosen from the manifest the component was read from rather than from its
// ecosystem, because that is the finer answer where one exists: a JavaScript
// project's ecosystem is "npm" whichever of four package managers put the
// tree there, and `npm why` in a pnpm repository is a wasted paste.
func whyCommand(c component) string {
	pkg := c.name
	switch filepath.Base(c.source) {
	case "go.mod":
		return "go mod why -m " + pkg
	case "package-lock.json":
		return "npm why " + pkg
	case "pnpm-lock.yaml":
		return "pnpm why " + pkg
	case "yarn.lock":
		return "yarn why " + pkg
	case "bun.lock", "bun.lockb":
		return "bun pm why " + pkg
	case "uv.lock":
		return "uv tree --invert --package " + pkg
	case "poetry.lock":
		return "poetry show --tree"
	case "Pipfile.lock":
		return "pipenv graph --reverse"
	case "Cargo.lock":
		return "cargo tree --invert --package " + pkg
	case "composer.lock":
		return "composer depends " + pkg
	case "Gemfile.lock":
		return "gem dependency " + pkg + " --reverse-dependencies"
	case "requirements.txt":
		return "pipdeptree --reverse --packages " + pkg
	}
	// An SBOM, or a filename nothing recognises. The ecosystem is the coarser
	// answer and it is still better than none.
	switch c.ecosystem {
	case "Go":
		return "go mod why -m " + pkg
	case "npm":
		return "npm why " + pkg
	case "PyPI":
		return "pipdeptree --reverse --packages " + pkg
	case "crates.io":
		return "cargo tree --invert --package " + pkg
	case "RubyGems":
		return "gem dependency " + pkg + " --reverse-dependencies"
	case "Packagist":
		return "composer depends " + pkg
	case "Maven":
		return "mvn dependency:tree -Dincludes=" + pkg
	case "NuGet":
		return "dotnet list package --include-transitive"
	}
	return ""
}

// unreadableManifest is a manifest that was found and could not be read. The
// reason travels with it: "bun.lockb could not be parsed" sends somebody
// looking for a bug, while "binary lockfile, run bun install
// --save-text-lockfile" is a thing they can do in ten seconds.
type unreadableManifest struct {
	path   string
	reason string
}

// inventory is everything the scan read off disk, before anything was asked
// about it.
//
// One value rather than six parameters, and the grouping is the argument:
// these are the facts a manifest states, they are always produced together by
// read(), and the grading reads them together. The alternative was a nine-
// argument gradeDeps whose call sites differed by which nils they passed —
// which is how a test comes to exercise a shape the caller never produces.
type inventory struct {
	// all is every component read, deduplicated and sorted.
	all []component
	// queryable and unknown split all by whether OSV could be asked at all.
	queryable []component
	unknown   []component
	// unreadable is what was found and could not be parsed.
	unreadable []unreadableManifest
	// manifests is every file that was looked at, readable or not.
	manifests []string
	// structure is what those files record about how they fit together.
	structure graph
	// truncated records that the flat component list hit maxComponents and
	// the rest of a manifest's declared components were not read. Its own
	// field rather than reusing structure.truncated: the two bounds fire
	// independently (a manifest can be edge-light and component-heavy, or
	// the reverse), and collapsing them would report the wrong one hit.
	truncated bool
	// ambiguous names the packages this project holds more than one version
	// of. A fact about the inventory rather than about any one manifest, and
	// the thing that decides whether a name-keyed answer may be repeated: a
	// tree with three copies of commander has a direct dependency on one of
	// them, and nothing in the file says which.
	ambiguous map[string]bool
}

// relation is how the project came by this component.
func (inv inventory) relation(c component) string {
	return inv.structure.relation(c, inv.ambiguous)
}

// read parses every manifest into one inventory.
// names are fs paths to read; shown is the same list as the reader should
// see it, index for index. Two slices rather than a struct because every
// consumer downstream wants only the second.
// maxComponents bounds the flat component list one run holds in memory. It
// is fed by the same untrusted lockfiles and SBOMs the edge graph is — up to
// and including one read from an in-memory clone of an arbitrary --path
// repository URL — and is at least as large, so it gets the same
// order-of-magnitude bound and the same announced-rather-than-silent
// treatment maxEdges already gets.
const maxComponents = 200000

func read(fsys fs.FS, names, shown []string) inventory {
	inv := inventory{manifests: shown, structure: newGraph()}
	var comps []component
	for i, m := range names {
		got, g, err := parseManifest(fsys, m, shown[i])
		if err != nil {
			inv.unreadable = append(inv.unreadable,
				unreadableManifest{path: shown[i], reason: clip(err.Error())})
			continue
		}
		if room := maxComponents - len(comps); len(got) > room {
			got = got[:max(0, room)]
			inv.truncated = true
		}
		comps = append(comps, got...)
		inv.structure.merge(g)
	}
	inv.all = dedupe(comps)
	sort.Slice(inv.all, func(i, j int) bool { return inv.all[i].key() < inv.all[j].key() })

	// A component whose ecosystem we could not name cannot be asked about.
	// It is counted and declared rather than dropped: a report that quietly
	// covered less than it appeared to is the failure mode that matters here.
	versions := map[string]map[string]bool{}
	for _, c := range inv.all {
		if c.ecosystem == "" {
			inv.unknown = append(inv.unknown, c)
			continue
		}
		inv.queryable = append(inv.queryable, c)
		r := ref(c.ecosystem, c.name)
		if versions[r] == nil {
			versions[r] = map[string]bool{}
		}
		versions[r][c.version] = true
	}
	inv.ambiguous = map[string]bool{}
	for r, vs := range versions {
		if len(vs) > 1 {
			inv.ambiguous[r] = true
		}
	}
	return inv
}

// gradeDeps turns the inventory and OSV's answer into findings. Pure, so the
// judgement is testable without a network or a filesystem.
// everyAdvisory is the distinct identifiers across every affected package,
// which is what a detail pass has to ask about — the same advisory naming
// four packages is one request, not four.
func everyAdvisory(vulns map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ids := range vulns {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out
}

func gradeDeps(r *report, inv inventory, vulns map[string][]string,
	records map[string]osvRecord, capped, offline bool) {
	for _, m := range inv.unreadable {
		r.add(grpInventory, "manifest", stWarn,
			m.path+" could not be read, so nothing in it was checked: "+m.reason, refVulnerableDep)
	}

	if len(inv.all) == 0 {
		r.add(grpInventory, "dependencies", stWarn,
			"found "+plural(len(inv.manifests), "manifest")+" but no pinned dependencies in them — "+
				"a requirements.txt of ranges names no version to check", refUnpinnedDep)
		return
	}

	// Vulnerable packages first, one finding each: a dependency is the unit
	// somebody upgrades, so it is the unit the report is built from.
	affected := make([]component, 0, len(vulns))
	for _, c := range inv.queryable {
		if len(vulns[c.key()]) > 0 {
			affected = append(affected, c)
		}
	}
	for _, c := range affected {
		classes := classify(vulns[c.key()], records)
		detail := c.name + " " + c.version + " (" + c.ecosystem + ")"
		if where := whereFrom(inv, c); where != "" {
			detail += " — " + where
		}
		detail += " — " + advisoryLine(c, classes)
		// Linked to the worst rather than the first: classify sorts by grade,
		// so the page this opens is the one that says how bad this is.
		r.addLinked(grpVulnerable, c.name, stFail, detail, refVulnerableDep, osvURL(classes[0].id))
	}
	switch {
	case offline:
		r.add(grpInventory, "advisories", stInfo,
			"not checked — --offline inventories the dependencies without asking osv.dev about them",
			refVulnerableDep)
	case len(affected) == 0 && len(inv.queryable) > 0:
		r.add(grpInventory, "advisories", stOK,
			"none of the "+strconv.Itoa(len(inv.queryable))+" checked dependencies is named in an OSV advisory",
			refVulnerableDep)
	case len(affected) > 0 && capped:
		// Never silently: a capped or timed-out detail pass leaves some rows
		// ungraded, and a blank severity that means "not asked" reads exactly
		// like one that means "nobody published a grade".
		r.add(grpInventory, "grading", stWarn,
			"stopped after "+strconv.Itoa(osvDetailMax)+" advisories or at the --timeout, so some rows "+
				"below are counted but not graded — raise --timeout, or run osv-scanner, trivy or grype "+
				"for a full pass",
			refVulnerableDep)
	case len(affected) > 0:
		r.add(grpInventory, "next step", stInfo,
			"severity and fixed versions come from osv.dev's own records. Run osv-scanner, trivy or "+
				"grype against this project for reachability — whether your code calls the vulnerable "+
				"function at all, which no advisory can say",
			refVulnerableDep)
	}
	if len(affected) > 0 {
		gradeProvenance(r, affected, inv)
	}

	byEco := map[string]int{}
	for _, c := range inv.queryable {
		byEco[c.ecosystem]++
	}
	ecos := make([]string, 0, len(byEco))
	for e := range byEco {
		ecos = append(ecos, e+" "+strconv.Itoa(byEco[e]))
	}
	sort.Strings(ecos)
	r.add(grpInventory, "dependencies", stInfo,
		strconv.Itoa(len(inv.all))+" declared across "+plural(len(inv.manifests), "manifest")+
			": "+strings.Join(ecos, ", ")+structureSummary(inv), refVulnerableDep)

	if len(inv.unknown) > 0 {
		names := make([]string, 0, len(inv.unknown))
		for _, c := range inv.unknown {
			names = append(names, c.name)
		}
		r.add(grpInventory, "unchecked", stWarn,
			plural(len(inv.unknown), "component")+" declare no ecosystem OSV recognises, so nothing was "+
				"asked about them: "+strings.Join(names, ", "), refVulnerableDep)
	}
}

// structureSummary is the split of the inventory the manifests were able to
// account for, appended to the dependency count it qualifies.
//
// It sits on the inventory line rather than in a finding of its own because
// it is a fact about the count beside it: "103 declared" reads very
// differently when 12 of them are yours and 91 came along.
func structureSummary(inv inventory) string {
	direct, indirect := 0, 0
	for _, c := range inv.all {
		switch inv.relation(c) {
		case relDirect:
			direct++
		case relIndirect:
			indirect++
		}
	}
	if direct == 0 && indirect == 0 {
		return "" // nothing read said which is which
	}
	out := " — " + strconv.Itoa(direct) + " asked for by name, " + strconv.Itoa(indirect) + " pulled in"
	if rest := len(inv.all) - direct - indirect; rest > 0 {
		out += ", " + strconv.Itoa(rest) + " not stated"
	}
	return out
}

// manifestSummary says what was read, in whichever of the two ways is
// informative.
//
// A few manifests are named individually, relative to the scanned path — for
// one project that is "go.mod", and for a monorepo it is the thing the reader
// actually needs, which is which package each one belongs to. Past that,
// listing them stops being a list and becomes a wall: --recursive over a
// twenty-repository tree printed "pnpm-lock.yaml, uv.lock, pnpm-lock.yaml,
// pnpm-lock.yaml, uv.lock, …" twenty-three times, which names nothing and
// hides the one fact that line could carry — which ecosystems are in here.
func manifestSummary(root string, manifests []string) string {
	const nameIndividually = 6
	if len(manifests) <= nameIndividually {
		out := make([]string, 0, len(manifests))
		for _, p := range manifests {
			if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
				p = rel
			}
			out = append(out, p)
		}
		return strings.Join(out, ", ")
	}
	byName := map[string]int{}
	for _, p := range manifests {
		byName[filepath.Base(p)]++
	}
	kinds := make([]string, 0, len(byName))
	for name := range byName {
		kinds = append(kinds, name)
	}
	// Commonest first: it is the one that says what kind of repository this is.
	sort.Slice(kinds, func(i, j int) bool {
		if byName[kinds[i]] != byName[kinds[j]] {
			return byName[kinds[i]] > byName[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	out := make([]string, 0, len(kinds))
	for _, name := range kinds {
		out = append(out, strconv.Itoa(byName[name])+"× "+name)
	}
	return strconv.Itoa(len(manifests)) + " across the tree: " + strings.Join(out, ", ")
}
