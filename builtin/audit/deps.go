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

	"github.com/this-is-tobi/rule-them-all/pkg/plugin"
	"github.com/this-is-tobi/rule-them-all/pkg/view"
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
const (
	grpVulnerable = "known vulnerabilities"
	grpInventory  = "inventory"
)

var depsGroupOrder = []string{grpVulnerable, grpInventory}

func runDeps(ctx context.Context, req plugin.Request) (view.View, error) {
	path := strings.TrimSpace(req.String("path"))
	if path == "" {
		path = "."
	}
	recursive := req.Bool("recursive")
	manifests, truncated, err := findManifests(path, recursive)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, view.Errorf("audit.deps.nopath", "no such path: %s", path).
				WithHint("pass the directory holding the lockfile or SBOM, or the file itself")
		}
		return nil, view.Errorf("audit.deps.path", "reading %s: %v", path, err)
	}
	if len(manifests) == 0 {
		hint := "reads what a project already declares, so one of these has to exist: " +
			strings.Join(ecosystems, "; ")
		if !recursive {
			hint += ". In a monorepo the manifests are a level down: try --recursive"
		}
		return nil, view.Errorf("audit.deps.nomanifest", "no lockfile or SBOM in %s", path).
			WithHint(hint)
	}

	var comps []component
	var unreadable []unreadableManifest
	for _, m := range manifests {
		got, err := parseManifest(m)
		if err != nil {
			unreadable = append(unreadable, unreadableManifest{path: m, reason: clip(err.Error())})
			continue
		}
		comps = append(comps, got...)
	}
	comps = dedupe(comps)
	sort.Slice(comps, func(i, j int) bool { return comps[i].key() < comps[j].key() })

	// A component whose ecosystem we could not name cannot be asked about.
	// It is counted and declared rather than dropped: a report that quietly
	// covered less than it appeared to is the failure mode that matters here.
	var queryable, unknown []component
	for _, c := range comps {
		if c.ecosystem == "" {
			unknown = append(unknown, c)
			continue
		}
		queryable = append(queryable, c)
	}

	r := &report{}
	offline := req.Bool("offline")
	var vulns map[string][]string
	if !offline && len(queryable) > 0 {
		timeout := time.Duration(req.Int("timeout")) * time.Second
		qctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		vulns, err = queryOSV(qctx, &stdhttp.Client{Timeout: timeout}, queryable)
		if err != nil {
			return nil, view.Errorf("audit.deps.osv", "querying osv.dev: %v", err).
				WithHint("use --offline to inventory the dependencies without asking anything")
		}
	}

	gradeDeps(r, comps, queryable, unknown, unreadable, manifests, vulns, offline)
	if truncated {
		r.add(grpInventory, "scan", stWarn,
			"stopped at "+strconv.Itoa(maxManifests)+" manifests or "+strconv.Itoa(maxScanDepth)+
				" directory levels, so this covers part of the tree — narrow the path to audit the rest",
			refVulnerableDep)
	}

	if req.Bool("detail") {
		summary := append([]view.Pair{
			{Key: "path", Value: path},
			{Key: "manifests", Value: manifestSummary(path, manifests)},
			{Key: "dependencies", Value: strconv.Itoa(len(comps))},
		}, r.grade()...)
		return detailPage(ctx, req, r, depsGroupOrder, view.KeyValue{Pairs: summary}), nil
	}
	return r.table(true), nil
}

// unreadableManifest is a manifest that was found and could not be read. The
// reason travels with it: "bun.lockb could not be parsed" sends somebody
// looking for a bug, while "binary lockfile, run bun install
// --save-text-lockfile" is a thing they can do in ten seconds.
type unreadableManifest struct {
	path   string
	reason string
}

// gradeDeps turns the inventory and OSV's answer into findings. Pure, so the
// judgement is testable without a network or a filesystem.
func gradeDeps(r *report, all, queryable, unknown []component, unreadable []unreadableManifest,
	manifests []string, vulns map[string][]string, offline bool) {

	for _, m := range unreadable {
		r.add(grpInventory, "manifest", stWarn,
			m.path+" could not be read, so nothing in it was checked: "+m.reason, refVulnerableDep)
	}

	if len(all) == 0 {
		r.add(grpInventory, "dependencies", stWarn,
			"found "+plural(len(manifests), "manifest")+" but no pinned dependencies in them — "+
				"a requirements.txt of ranges names no version to check", refUnpinnedDep)
		return
	}

	// Vulnerable packages first, one finding each: a dependency is the unit
	// somebody upgrades, so it is the unit the report is built from.
	affected := make([]component, 0, len(vulns))
	for _, c := range queryable {
		if len(vulns[c.key()]) > 0 {
			affected = append(affected, c)
		}
	}
	for _, c := range affected {
		ids := vulns[c.key()]
		sort.Strings(ids)
		detail := c.name + " " + c.version + " (" + c.ecosystem + ") is named in " +
			plural(len(ids), "advisory") + ": " + strings.Join(ids, ", ") + " — " + osvURL(ids[0])
		r.add(grpVulnerable, c.name, stFail, detail, refVulnerableDep)
	}

	switch {
	case offline:
		r.add(grpInventory, "advisories", stInfo,
			"not checked — --offline inventories the dependencies without asking osv.dev about them",
			refVulnerableDep)
	case len(affected) == 0 && len(queryable) > 0:
		r.add(grpInventory, "advisories", stOK,
			"none of the "+strconv.Itoa(len(queryable))+" checked dependencies is named in an OSV advisory",
			refVulnerableDep)
	case len(affected) > 0:
		// The identifiers are what one batch query can answer. Severity and
		// fixed versions are one request per advisory, which is the crawl
		// this plugin does not do — so it says where to go instead.
		r.add(grpInventory, "next step", stInfo,
			"advisory IDs only: osv.dev's batch endpoint does not carry severity or fixed versions. "+
				"Run osv-scanner, trivy or grype against this project for those",
			refVulnerableDep)
	}

	byEco := map[string]int{}
	for _, c := range queryable {
		byEco[c.ecosystem]++
	}
	ecos := make([]string, 0, len(byEco))
	for e := range byEco {
		ecos = append(ecos, e+" "+strconv.Itoa(byEco[e]))
	}
	sort.Strings(ecos)
	r.add(grpInventory, "dependencies", stInfo,
		strconv.Itoa(len(all))+" declared across "+plural(len(manifests), "manifest")+
			": "+strings.Join(ecos, ", "), refVulnerableDep)

	if len(unknown) > 0 {
		names := make([]string, 0, len(unknown))
		for _, c := range unknown {
			names = append(names, c.name)
		}
		r.add(grpInventory, "unchecked", stWarn,
			plural(len(unknown), "component")+" declare no ecosystem OSV recognises, so nothing was "+
				"asked about them: "+strings.Join(names, ", "), refVulnerableDep)
	}
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
