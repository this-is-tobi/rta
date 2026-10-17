package audit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// catalogueIndex is the name index of a catalogue that lists these products.
func catalogueIndex(products ...string) map[string]string {
	index := map[string]string{"postgres": "postgresql"}
	for _, p := range products {
		index[p] = p
	}
	return index
}

func use(t *testing.T, ref string) *imageUse {
	t.Helper()
	var pods list[podImageItem]
	body := jsonBody(t, obj{"items": []obj{{"metadata": obj{"namespace": "a", "name": "p"},
		"spec": obj{"containers": []obj{{"image": ref}}}}}})
	if err := json.Unmarshal([]byte(body), &pods); err != nil {
		t.Fatal(err)
	}
	images := collectImages(pods.Items)
	if len(images) != 1 {
		t.Fatalf("%q collected as %d images", ref, len(images))
	}
	return images[0]
}

func resolveWith(t *testing.T, operator []string, ref string, products ...string) (productOrDash string, ok bool) {
	t.Helper()
	rules, verr := parseImageRules(operator)
	if verr != nil {
		t.Fatal(verr)
	}
	m, verr := newImageMap(rules, catalogueIndex(products...))
	if verr != nil {
		t.Fatal(verr)
	}
	rule, ok := m.resolve(use(t, ref))
	if rule.exclude {
		return "-", ok
	}
	return rule.product, ok
}

// Recognition was the image's last path segment against the catalogue, so a
// piece of a product named for the piece (cert-manager-controller) was never
// the product. The table says which pieces share the product's version, and it
// keys on the repository's tail so a mirror in front of it changes nothing.
func TestImageTableResolvesComponentsByBasenameAndSuffix(t *testing.T) {
	products := []string{"cert-manager", "kyverno", "cilium", "kubernetes", "longhorn"}
	for _, tc := range []struct {
		name     string
		operator []string
		ref      string
		want     string
		mapped   bool
	}{
		{"a component by its basename", nil, "quay.io/jetstack/cert-manager-controller:v1.19.1", "cert-manager", true},
		{"through a mirror prefix", nil, "cache.example.com/docker.io/jetstack/cert-manager-controller:v1.19.1", "cert-manager", true},
		{"with no registry at all", nil, "cert-manager-webhook:v1.19.1", "cert-manager", true},
		{"a generic name is qualified by its organisation", nil, "ghcr.io/kyverno/background-controller:v1.14.0", "kyverno", true},
		{"and another project's generic name is not the product's", nil, "ghcr.io/acme/background-controller:v1.14.0", "", false},
		{"cilium's own operator", nil, "quay.io/cilium/operator-generic:v1.18.2", "cilium", true},
		{"a piece that versions apart is not in the table", nil, "quay.io/cilium/hubble-ui:v0.13.2", "", false},
		{"an operator entry names a product", []string{"acme/app=postgres"}, "ghcr.io/acme/app:1.2", "postgresql", true},
		{"an operator entry beats the table", []string{"cert-manager-controller=postgres"},
			"quay.io/jetstack/cert-manager-controller:v1.19.1", "postgresql", true},
		{"the longest operator suffix wins", []string{"cert-manager-controller=cert-manager", "me/cert-manager-controller=-"},
			"ghcr.io/me/cert-manager-controller:v1", "-", true},
		{"matching is on path segments", []string{"app=-"}, "ghcr.io/acme/myapp:1", "", false},
		{"and ignores case", []string{"ACME/App=-"}, "ghcr.io/acme/app:1", "-", true},
		{"a suffix may name the registry", []string{"ghcr.io/me/app=-"}, "ghcr.io/me/app:1", "-", true},
		{"a registry the image is not on does not match", []string{"ghcr.io/me/app=-"}, "quay.io/me/app:1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveWith(t, tc.operator, tc.ref, products...)
			if got != tc.want || ok != tc.mapped {
				t.Errorf("resolve(%q) = %q, %v; want %q, %v", tc.ref, got, ok, tc.want, tc.mapped)
			}
		})
	}
}

// The table is a claim about other people's release schemes, so an entry whose
// product the site does not list is dropped rather than trusted: the site
// renames a product, and the cost is a row not graded and not a wrong verdict.
func TestATableEntryForAProductTheCatalogueLacksIsIgnored(t *testing.T) {
	got, ok := resolveWith(t, nil, "quay.io/jetstack/cert-manager-controller:v1.19.1", "kubernetes")
	if ok {
		t.Errorf("resolved to %q although the catalogue lists no cert-manager", got)
	}
}

// The table is what turns the piece into the product in a real run: a
// component graded as cert-manager, through a mirror, with no setting at all.
func TestKubeEOLGradesAComponentAsItsProductFromTheTable(t *testing.T) {
	out := runEOL(t, podsRunning(t,
		"quay.io/jetstack/cert-manager-controller:v1.19.1",
		"mirror.corp/jetstack/cert-manager-webhook:v1.19.1"), nil)
	rendered := renderText(t, out)
	for _, want := range []string{
		"image jetstack/cert-manager-controller:v1.19.1 (1 pod in 1 namespace) | ok | cert-manager 1.19 is supported until 2099-06-10",
		"image jetstack/cert-manager-webhook:v1.19.1 (1 pod in 1 namespace) | ok | cert-manager 1.19 is supported",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}
}

// A table entry for a product the site does not list asks the site nothing and
// grades nothing: kyverno is not in the fixture's catalogue.
func TestKubeEOLIgnoresATableEntryWhoseProductTheSiteDoesNotList(t *testing.T) {
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
		"pods": podsRunning(t, "ghcr.io/kyverno/kyvernopre:v1.14.0")})
	srv, requests := newEOLServer(t)
	out, err := runKubeEOLAt(t.Context(), eolRequest(nil).With(map[string]any{"detail": true}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if row := notGradedByImage(t, out)["kyverno/kyvernopre:v1.14.0"]; row == nil || row[2] != "no-product" {
		t.Errorf("row = %q, want it not graded for want of a product", row)
	}
	if *requests != 2 {
		t.Errorf("%d requests, want 2 (the catalogue and kubernetes): a product the site does not list is not asked for", *requests)
	}
}

func TestOperatorMappingToAnUnknownProductIsRefused(t *testing.T) {
	rules, verr := parseImageRules([]string{"acme/app=nosuchproduct"})
	if verr != nil {
		t.Fatal(verr)
	}
	_, verr = newImageMap(rules, catalogueIndex("postgresql"))
	if verr == nil || verr.Code != "audit.kube.eol.images.unknown" {
		t.Fatalf("got %v, want audit.kube.eol.images.unknown", verr)
	}
	if !strings.Contains(verr.Message, "acme/app=nosuchproduct") {
		t.Errorf("message %q does not name the entry", verr.Message)
	}
}

func TestMalformedOperatorEntriesAreRefused(t *testing.T) {
	for _, bad := range []string{"", "noequals", "=postgres", "acme/app=", "acme app=postgres", "acme/app=a=b", "//=-", "acme/../app=-"} {
		if _, verr := parseImageRules([]string{bad}); verr == nil || verr.Code != "audit.kube.eol.images.invalid" {
			t.Errorf("%q: got %v, want audit.kube.eol.images.invalid", bad, verr)
		}
	}
	if _, verr := parseImageRules([]string{"acme/app=-", "ACME/app=postgres"}); verr == nil {
		t.Error("the same suffix twice with two meanings was accepted")
	}
	for _, ok := range []string{"acme/app=postgres", " acme/app = - ", "ghcr.io/me/app=-", "registry:5000/team/app=-"} {
		if _, verr := parseImageRules([]string{ok}); verr != nil {
			t.Errorf("%q was refused: %v", ok, verr)
		}
	}
}

// The table is read at build time by nobody but this package, so the only
// reviewer of its shape is this test.
func TestEmbeddedImageTableIsWellFormed(t *testing.T) {
	rules := builtinImageRules()
	if len(rules) < 10 {
		t.Fatalf("%d entries; the table was not read", len(rules))
	}
	// A line the parser skips is a line that does nothing, and nobody sees it.
	var lines int
	for _, line := range strings.Split(builtinImageTable, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "#") {
			lines++
		}
	}
	if lines != len(rules) {
		t.Errorf("%d entry lines and %d rules: a line was not read", lines, len(rules))
	}
	seen := map[string]bool{}
	for _, r := range rules {
		switch {
		case r.suffix != strings.ToLower(r.suffix) || r.product != strings.ToLower(r.product):
			t.Errorf("%q is not lowercase", r.text)
		case r.product == "" || r.exclude:
			t.Errorf("%q names no product: the table grades, it does not exclude", r.text)
		case seen[r.suffix]:
			t.Errorf("%q is listed twice", r.suffix)
		}
		seen[r.suffix] = true
	}
}

func TestSplitImageIsTheRepositoryWithoutItsRegistry(t *testing.T) {
	for _, tc := range []struct{ ref, full, repo, tag string }{
		{"postgres:15.4", "postgres", "postgres", "15.4"},
		{"docker.io/library/postgres:15.4", "docker.io/library/postgres", "postgres", "15.4"},
		{"cache.k8s.fr-par.scw.cloud/docker.io/cilium/cilium:v1.18", "cache.k8s.fr-par.scw.cloud/docker.io/cilium/cilium", "cilium/cilium", "v1.18"},
		{"registry:5000/team/App:v2", "registry:5000/team/app", "team/app", "v2"},
		{"localhost/app:1", "localhost/app", "app", "1"},
		{"bitnami/redis:7.2", "bitnami/redis", "bitnami/redis", "7.2"},
		{"quay.io/foo/redis@sha256:0123", "quay.io/foo/redis", "foo/redis", ""},
	} {
		full, repo, tag, _ := splitImage(tc.ref)
		if full != tc.full || repo != tc.repo || tag != tc.tag {
			t.Errorf("splitImage(%q) = %q, %q, %q; want %q, %q, %q", tc.ref, full, repo, tag, tc.full, tc.repo, tc.tag)
		}
	}
}

// Two projects sharing a last path segment are two images. Keyed by the
// segment, bitnami/redis and the official redis were one row, graded as one
// product, and a fork called postgres could not be left out without leaving
// the real one out with it.
func TestCollectImagesKeepsTwoProjectsWithTheSameBasenameApart(t *testing.T) {
	var pods list[podImageItem]
	body := jsonBody(t, obj{"items": []obj{
		{"metadata": obj{"namespace": "a", "name": "p1"}, "spec": obj{"containers": []obj{{"image": "bitnami/redis:7.2"}}}},
		{"metadata": obj{"namespace": "a", "name": "p2"}, "spec": obj{"containers": []obj{{"image": "redis:7.2"}}}},
		{"metadata": obj{"namespace": "b", "name": "p3"}, "spec": obj{"containers": []obj{{"image": "docker.io/library/redis:7.2"}}}},
	}})
	if err := json.Unmarshal([]byte(body), &pods); err != nil {
		t.Fatal(err)
	}
	images := collectImages(pods.Items)
	if len(images) != 2 {
		t.Fatalf("%d images, want 2 (bitnami/redis and redis): %+v", len(images), images)
	}
	byRepo := map[string]*imageUse{}
	for _, u := range images {
		byRepo[u.repo] = u
	}
	// The official image under two spellings is one image on two pods.
	if u := byRepo["redis"]; u == nil || u.pods != 2 || len(u.namespaces) != 2 {
		t.Errorf("redis = %+v, want 2 pods in 2 namespaces", u)
	}
	if u := byRepo["bitnami/redis"]; u == nil || u.pods != 1 {
		t.Errorf("bitnami/redis = %+v", u)
	}
}

// An image the operator says is theirs is left out and said to be: it is not
// a check that could not run, so the overall does not count it, and it is not
// silently gone either.
func TestOperatorMappingCanExcludeAnImage(t *testing.T) {
	pods := podsRunning(t, "ghcr.io/me/app:1.0", "other/thing:2", "postgres:15.4")
	out := runEOL(t, pods, map[string]any{"images": []string{"ghcr.io/me/app=-"}})

	rendered := renderText(t, out)
	for _, want := range []string{
		"images excluded (1) | info |",
		// 3 run, 1 is left out, 1 is graded, and one could not be: 1 of the 2
		// that were in question.
		"images not graded (1 of 2) | info | 1 not on endoflife.date",
		"1 check could not run",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}

	rows := notGradedByImage(t, runEOL(t, pods, map[string]any{"images": []string{"ghcr.io/me/app=-"}, "detail": true}))
	row, ok := rows["me/app:1.0"]
	if !ok {
		t.Fatalf("the excluded image is not listed: %v", imageNames(rows))
	}
	if row[2] != "excluded" || !strings.Contains(row[3], "ghcr.io/me/app=-") {
		t.Errorf("row = %q, want the reason excluded and the entry that did it", row)
	}
}

// A mapping made on a name the operator chose is graded as the product they
// named, whatever the image is called.
func TestOperatorMappingGradesAnImageAsTheProductNamed(t *testing.T) {
	out := runEOL(t, podsRunning(t, "registry.corp/platform/cache-engine:6.2.1"),
		map[string]any{"images": []string{"platform/cache-engine=redis"}})
	if rendered := renderText(t, out); !strings.Contains(rendered,
		"image platform/cache-engine:6.2.1 (1 pod in 1 namespace) | fail | redis 6.2 reached end of life") {
		t.Errorf("the mapped image was not graded as redis:\n%s", rendered)
	}
}

func TestAnUnknownProductInTheImagesSettingRefusesTheRunBeforeAnyPageIsAsked(t *testing.T) {
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
		"pods": podsRunning(t, "me/app:1")})
	srv, requests := newEOLServer(t)
	req := eolRequest(map[string]any{"images": []string{"me/app=nosuchproduct"}})

	_, err := runKubeEOLAt(t.Context(), req, srv.URL)
	if ve := view.AsError(err, "x"); err == nil || ve.Code != "audit.kube.eol.images.unknown" {
		t.Fatalf("err = %v, want audit.kube.eol.images.unknown", err)
	}
	if *requests != 1 {
		t.Errorf("%d requests: only the catalogue, which the entry is checked against, should have been asked", *requests)
	}
}

// A mapping is the operator's policy, not something a caller supplies: it
// chooses which fixed-host page is asked about, and over MCP the audit would
// otherwise let a model aim it.
func TestTheImagesSettingIsNeverOfferedToACaller(t *testing.T) {
	for _, c := range Plugin(nil, nil).Capabilities {
		if c.ID != "audit.kube.eol" {
			continue
		}
		for _, f := range c.Inputs {
			if f.Name == "images" {
				if !f.Local || f.Config != "images" || f.Type != plugin.StringSlice {
					t.Errorf("images = %+v, want a Local, config-backed list", f)
				}
				return
			}
		}
	}
	t.Fatal("audit.kube.eol declares no images input")
}
