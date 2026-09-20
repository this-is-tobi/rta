package audit

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// ---- audit.kube.eol ----
//
// The other four kube.* audits grade configuration; this one grades age. A
// cluster runs a control plane at some version, kubelets at some version,
// and a few hundred containers each pulled from an image whose tag says
// which release of which product it is — and every one of those has an
// end-of-life date on endoflife.date, the same public API the eol built-in
// reads. The question a platform team gets at every review, "is anything
// we run out of support", is otherwise answered by somebody pasting image
// tags into a browser one at a time, which is why it gets asked yearly
// rather than weekly.
//
// What it reads is two `kubectl get` calls and `kubectl version`, the same
// shell-out as its siblings (kubectl.go), plus one request to endoflife.date
// per distinct product found — never per pod: a hundred postgres pods at
// one tag are one lookup and one row. Recognising an image is deliberately
// no cleverer than the catalogue: the image's name (postgres, redis, nginx)
// is looked up among the products' own names and aliases, and an image
// that is not there is reported as not tracked rather than guessed at. The
// tag is read as a release the way a person reads it — 15.4 is cycle 15,
// 1.35.3 is cycle 1.35, bookworm is the cycle codenamed Bookworm — and a
// tag that names no cycle, or no tag at all, says so in its own row.

var (
	grpKubeEOL      = findings.Group{ID: "end-of-life", Title: "end of life"}
	kubeGroupOrder5 = []findings.Group{grpKubeEOL}
)

// maxEOLProducts bounds the lookups one call makes. endoflife.date has no
// batch endpoint, so every distinct product in the cluster is one request;
// the eol built-in's watchlist stops at twenty for the same reason. A
// cluster runs a handful of distinct tracked products in practice, so
// thirty is room rather than a limit anybody meets — and one that does is
// graded on the first thirty with a finding naming the rest, instead of a
// call that runs for a minute.
const maxEOLProducts = 30

// defaultEOLWarnDays is the eol built-in's own window, for the same reason
// it is wider than cert.expiry's: a major-version upgrade is not something
// anybody does in an afternoon.
const defaultEOLWarnDays = 90

func warnDaysField() plugin.Field {
	return plugin.Field{Name: "warn-days", Type: plugin.Int, Config: "warn-days", Default: defaultEOLWarnDays,
		Help: "warn about a release within this many days of its end-of-life date"}
}

type serverVersion struct {
	ServerVersion struct {
		GitVersion string `json:"gitVersion"`
	} `json:"serverVersion"`
}

type nodeVersionItem struct {
	Metadata meta `json:"metadata"`
	Status   struct {
		NodeInfo struct {
			KubeletVersion string `json:"kubeletVersion"`
		} `json:"nodeInfo"`
	} `json:"status"`
}

type imageCtn struct {
	Image string `json:"image"`
}

type podImageItem struct {
	Metadata meta `json:"metadata"`
	Spec     struct {
		Containers          []imageCtn `json:"containers"`
		InitContainers      []imageCtn `json:"initContainers"`
		EphemeralContainers []imageCtn `json:"ephemeralContainers"`
	} `json:"spec"`
}

func runKubeEOL(ctx context.Context, req plugin.Request) (view.View, error) {
	return runKubeEOLAt(ctx, req, eolapi.APIBase)
}

func runKubeEOLAt(ctx context.Context, req plugin.Request, base string) (view.View, error) {
	kubeContext := req.String("context")
	ns, verr := scopeOf(req)
	if verr != nil {
		return nil, verr
	}
	r := &findings.Report{}

	// The cluster first, the API second: kubectl failing is about this
	// machine's access and the likelier of the two, and nothing is asked of
	// endoflife.date until there is something to ask about.
	var sv serverVersion
	if verr := kubeVersionJSON(ctx, kubeContext, &sv); verr != nil {
		return nil, verr
	}
	var nodes list[nodeVersionItem]
	if ns == "" {
		if verr := kubeGetJSON(ctx, kubeContext, "", "nodes", &nodes); verr != nil {
			return nil, verr
		}
	} else {
		// The same shape as runKubeRBAC's note, for the same reason: a
		// narrowed run that silently skipped the kubelets would read as a
		// cluster whose nodes are all in support.
		r.Add(grpKubeEOL, "kubelets not examined", findings.Info,
			"narrowed to namespace "+ns+", so the nodes' kubelet versions were not checked — "+
				"they belong to no namespace. Run without a namespace to include them.", refUnmaintained)
	}
	var pods list[podImageItem]
	if verr := kubeGetJSON(ctx, kubeContext, ns, "pods", &pods); verr != nil {
		return nil, verr
	}

	catalogue, verr := eolapi.FetchCatalogue(ctx, http.DefaultClient, base)
	if verr != nil {
		return nil, verr
	}
	g := &grader{ctx: ctx, base: base, warnDays: req.Int("warn-days"), now: time.Now(),
		index: productIndex(catalogue), fetched: map[string]*eolapi.Product{}, skipped: map[string]bool{}}

	if verr := g.gradeControlPlane(r, sv.ServerVersion.GitVersion); verr != nil {
		return nil, verr
	}
	if ns == "" {
		if verr := g.gradeKubelets(r, nodes.Items); verr != nil {
			return nil, verr
		}
	}
	if verr := g.gradeImages(r, collectImages(pods.Items), ns == ""); verr != nil {
		return nil, verr
	}
	if len(g.skipped) > 0 {
		names := sortedKeys(g.skipped)
		r.Add(grpKubeEOL, fmt.Sprintf("products not looked up (%d)", len(names)), findings.Info,
			fmt.Sprintf("more than %d distinct products run here, and one call looks up that many: %s",
				maxEOLProducts, strings.Join(names, ", ")), refUnmaintained)
	}

	if !req.Bool("detail") {
		return r.Table(true), nil
	}
	return r.Page(ctx, req, kubeGroupOrder5, r.Table(true)), nil
}

// grader looks each product up once and grades versions against it.
type grader struct {
	ctx      context.Context
	base     string
	warnDays int
	now      time.Time
	// index is every catalogue name and alias, lowercased, to the product
	// it names — what turns an image called postgres into postgresql.
	index map[string]string
	// fetched caches one lookup per product; nil for a product the
	// catalogue lists but the API has no page for.
	fetched map[string]*eolapi.Product
	// skipped names the products past maxEOLProducts, reported once.
	skipped map[string]bool
}

func (g *grader) product(name string) (*eolapi.Product, *view.Error) {
	if p, ok := g.fetched[name]; ok {
		return p, nil
	}
	if len(g.fetched) >= maxEOLProducts {
		g.skipped[name] = true
		return nil, nil
	}
	p, verr := eolapi.FetchProduct(g.ctx, http.DefaultClient, g.base, name)
	if verr != nil {
		if verr.Code == "eol.product.notfound" {
			g.fetched[name] = nil
			return nil, nil
		}
		return nil, verr
	}
	g.fetched[name] = p
	return p, nil
}

// grade is the verdict on one version of one product: a status and the
// sentence beside it, with the end-of-life date where there is one.
func (g *grader) grade(p *eolapi.Product, version string) (status, detail string) {
	rel, ok := cycleFor(p.Releases, version)
	if !ok {
		return findings.Info, fmt.Sprintf("%s names no release cycle of %s that endoflife.date knows (%s)",
			version, p.Name, cycleNames(p.Releases))
	}
	label := p.Name + " " + rel.Name
	switch eolapi.Grade(rel, g.warnDays, g.now) {
	case eolapi.Ended:
		if d, ok := eolapi.EolDate(rel); ok {
			return findings.Fail, fmt.Sprintf("%s reached end of life on %s, %s ago",
				label, *rel.EolFrom, findings.Plural(daysBetween(d, g.now), "day"))
		}
		return findings.Fail, label + " is past its end of life"
	case eolapi.Ending:
		d, _ := eolapi.EolDate(rel)
		return findings.Warn, fmt.Sprintf("%s reaches end of life on %s, in %s",
			label, *rel.EolFrom, findings.Plural(daysBetween(g.now, d), "day"))
	}
	if rel.EolFrom == nil {
		return findings.OK, label + " is supported, no end-of-life date announced"
	}
	return findings.OK, label + " is supported until " + *rel.EolFrom
}

func (g *grader) gradeControlPlane(r *findings.Report, gitVersion string) *view.Error {
	version := releaseOf(gitVersion)
	if version == "" {
		r.Add(grpKubeEOL, "control plane", findings.Info,
			"the API server reported no version to grade", refUnmaintained)
		return nil
	}
	p, verr := g.kubernetes()
	if verr != nil {
		return verr
	}
	if p == nil {
		r.Add(grpKubeEOL, "control plane: "+gitVersion, findings.Info, noKubernetesPage, refUnmaintained)
		return nil
	}
	status, detail := g.grade(p, version)
	r.AddLinked(grpKubeEOL, "control plane: "+gitVersion, status, detail, refUnmaintained, productPage(p))
	return nil
}

// noKubernetesPage is the row a version gets when the API has no page for
// kubernetes itself — which it has had since the site began, so this is a
// statement about the API being unreachable in a novel way rather than a
// case anybody expects. Said rather than skipped: a report with no control
// plane row at all reads as a control plane that was not asked about.
const noKubernetesPage = "endoflife.date has no release data for kubernetes, so this version cannot be graded"

// kubernetes is the product every cluster row grades against, looked up
// ahead of the cap: the images are numerous and optional, the control plane
// is one and the point.
func (g *grader) kubernetes() (*eolapi.Product, *view.Error) {
	if p, ok := g.fetched["kubernetes"]; ok {
		return p, nil
	}
	p, verr := eolapi.FetchProduct(g.ctx, http.DefaultClient, g.base, "kubernetes")
	if verr != nil {
		if verr.Code == "eol.product.notfound" {
			g.fetched["kubernetes"] = nil
			return nil, nil
		}
		return nil, verr
	}
	g.fetched["kubernetes"] = p
	return p, nil
}

// gradeKubelets groups nodes by kubelet version — a fleet at one version is
// one row, not one per node — and names the nodes in the detail.
func (g *grader) gradeKubelets(r *findings.Report, nodes []nodeVersionItem) *view.Error {
	byVersion := map[string][]string{}
	for _, n := range nodes {
		v := n.Status.NodeInfo.KubeletVersion
		byVersion[v] = append(byVersion[v], n.Metadata.Name)
	}
	if len(byVersion) == 0 {
		return nil
	}
	p, verr := g.kubernetes()
	if verr != nil {
		return verr
	}
	for _, v := range sortedKeys(byVersion) {
		names := byVersion[v]
		sort.Strings(names)
		check := fmt.Sprintf("kubelets at %s (%s)", v, findings.Plural(len(names), "node"))
		if v == "" {
			check = fmt.Sprintf("kubelets reporting no version (%s)", findings.Plural(len(names), "node"))
			r.Add(grpKubeEOL, check, findings.Info, "nodes: "+strings.Join(names, ", "), refUnmaintained)
			continue
		}
		if p == nil {
			r.Add(grpKubeEOL, check, findings.Info, noKubernetesPage+" — nodes: "+strings.Join(names, ", "), refUnmaintained)
			continue
		}
		status, detail := g.grade(p, releaseOf(v))
		r.AddLinked(grpKubeEOL, check, status, detail+" — nodes: "+strings.Join(names, ", "),
			refUnmaintained, productPage(p))
	}
	return nil
}

// imageUse is one distinct image reference and where it runs.
type imageUse struct {
	name, tag  string
	digest     bool
	pods       int
	namespaces map[string]bool
}

// collectImages folds every container of every pod — init and ephemeral
// included, since an init container's image is as much a release somebody
// runs as a main one's — into one entry per distinct name and tag, so a
// hundred replicas of one image are one row with a count.
func collectImages(pods []podImageItem) []*imageUse {
	byRef := map[string]*imageUse{}
	for _, p := range pods {
		seenInPod := map[string]bool{}
		all := append(append(append([]imageCtn{}, p.Spec.Containers...), p.Spec.InitContainers...), p.Spec.EphemeralContainers...)
		for _, c := range all {
			name, tag, digest := imageRef(c.Image)
			if name == "" {
				continue
			}
			key := name + ":" + tag
			if digest {
				key += "@"
			}
			u, ok := byRef[key]
			if !ok {
				u = &imageUse{name: name, tag: tag, digest: digest, namespaces: map[string]bool{}}
				byRef[key] = u
			}
			if !seenInPod[key] {
				seenInPod[key] = true
				u.pods++
			}
			u.namespaces[p.Metadata.Namespace] = true
		}
	}
	out := make([]*imageUse, 0, len(byRef))
	for _, u := range byRef {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].name != out[j].name {
			return out[i].name < out[j].name
		}
		return out[i].tag < out[j].tag
	})
	return out
}

func (g *grader) gradeImages(r *findings.Report, images []*imageUse, clusterWide bool) *view.Error {
	untracked := map[string]bool{}
	for _, u := range images {
		product, tracked := g.index[u.name]
		if !tracked {
			untracked[u.name] = true
			continue
		}
		check := "image " + u.name
		if u.tag != "" {
			check += ":" + u.tag
		}
		where := findings.Plural(u.pods, "pod")
		if clusterWide {
			where += " in " + findings.Plural(len(u.namespaces), "namespace")
		}
		check += " (" + where + ")"

		p, verr := g.product(product)
		if verr != nil {
			return verr
		}
		if p == nil {
			// Skipped past the cap, reported once at the end; or listed in
			// the catalogue with no page behind it, which is the API's own
			// inconsistency and worth one row rather than a failed call.
			if !g.skipped[product] {
				r.Add(grpKubeEOL, check, findings.Info,
					"endoflife.date lists "+product+" in its catalogue but has no release data for it", refUnmaintained)
			}
			continue
		}
		switch {
		case u.digest && u.tag == "":
			r.AddLinked(grpKubeEOL, check, findings.Info,
				"pinned by digest with no tag: a digest names bytes, not a release, so "+p.Name+
					"'s support window cannot be read from it", refUnmaintained, productPage(p))
		case u.tag == "" || strings.EqualFold(releaseOf(u.tag), "latest"):
			r.AddLinked(grpKubeEOL, check, findings.Info,
				"a floating tag names no release, so nothing can be said about "+p.Name+
					"'s support window — pin a version to grade it", refUnmaintained, productPage(p))
		default:
			status, detail := g.grade(p, releaseOf(u.tag))
			r.AddLinked(grpKubeEOL, check, status, detail, refUnmaintained, productPage(p))
		}
	}
	if len(untracked) > 0 {
		names := sortedKeys(untracked)
		r.Add(grpKubeEOL, fmt.Sprintf("images endoflife.date does not track (%d)", len(names)), findings.Info,
			strings.Join(names, ", "), refUnmaintained)
	}
	return nil
}

// imageRef splits a container image reference into the name people know it
// by and the version its tag claims: "ghcr.io/cloudnative-pg/postgresql:16.4-3"
// is (postgresql, 16.4-3). The registry and the path above the last segment
// say where it came from, not what it is. A digest is not a version — "@sha256:…"
// pins bytes, and the bytes' release is nobody's to read off the reference —
// so it is reported as such rather than mistaken for a tag.
func imageRef(ref string) (name, tag string, digest bool) {
	ref = strings.TrimSpace(ref)
	if at := strings.Index(ref, "@"); at >= 0 {
		digest = true
		ref = ref[:at]
	}
	last := ref
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		last = ref[i+1:]
	}
	name, tag, _ = strings.Cut(last, ":")
	return strings.ToLower(name), tag, digest
}

// releaseOf reads the release out of a tag or a build version the way a
// person does: "15.4-alpine" is 15.4, "v1.35.3+k3s1" is 1.35.3, "16.4-3" is
// 16.4, "bookworm-slim" is bookworm. Everything after the first separator is
// the variant or the build, which no support window is about.
func releaseOf(tag string) string {
	v := tag
	if i := strings.IndexAny(v, "-_+"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimPrefix(v, "v")
}

// cycleFor picks the release cycle a version belongs to: the cycle whose
// name the version equals or extends — 15.4 is cycle 15, 1.35.3 is cycle
// 1.35, 24.04.2 is cycle 24.04 — with the longest name winning, so 1.35
// beats 1 for 1.35.3. Failing that, the cycle whose codename the version
// is, for debian:bookworm and its kind.
func cycleFor(releases []eolapi.Release, version string) (eolapi.Release, bool) {
	var best eolapi.Release
	found := false
	for _, rel := range releases {
		if strings.EqualFold(rel.Name, version) || strings.HasPrefix(version, rel.Name+".") {
			if !found || len(rel.Name) > len(best.Name) {
				best, found = rel, true
			}
		}
	}
	if found {
		return best, true
	}
	for _, rel := range releases {
		if rel.Codename != "" && strings.EqualFold(rel.Codename, version) {
			return rel, true
		}
	}
	return eolapi.Release{}, false
}

// productIndex maps every catalogue name and alias, lowercased, to the
// product it names. A name wins over an alias of another product: the
// catalogue lists "postgres" as an alias of postgresql, and were some other
// product ever named postgres its own page is the right answer.
func productIndex(entries []eolapi.CatalogueEntry) map[string]string {
	index := map[string]string{}
	for _, e := range entries {
		for _, a := range e.Aliases {
			if a = strings.ToLower(a); a != "" {
				if _, taken := index[a]; !taken {
					index[a] = e.Name
				}
			}
		}
	}
	for _, e := range entries {
		if n := strings.ToLower(e.Name); n != "" {
			index[n] = e.Name
		}
	}
	return index
}

func productPage(p *eolapi.Product) string { return "https://endoflife.date/" + p.Name }

func cycleNames(releases []eolapi.Release) string {
	names := make([]string, len(releases))
	for i, r := range releases {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

func daysBetween(from, to time.Time) int { return int(to.Sub(from).Hours() / 24) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
