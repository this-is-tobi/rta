package audit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/this-is-tobi/rta/builtin/internal/eolapi"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
)

// newEOLServer answers like endoflife.date for the handful of products the
// cluster fixtures run: the catalogue with its aliases, then one page per
// product. Dates are far enough out, or far enough back, that the test does
// not move with the calendar; the one that must be near is computed.
func newEOLServer(t *testing.T, omit ...string) (*httptest.Server, *int) {
	t.Helper()
	requests := 0
	soon := time.Now().AddDate(0, 0, 40).Format("2006-01-02")
	pages := map[string]string{
		"/products": `{"result":[
			{"name":"kubernetes","label":"Kubernetes","category":"server-app","aliases":["k8s"]},
			{"name":"postgresql","label":"PostgreSQL","category":"database","aliases":["postgres","pg"]},
			{"name":"redis","label":"Redis","category":"database","aliases":[]},
			{"name":"nginx","label":"nginx","category":"server-app","aliases":[]},
			{"name":"debian","label":"Debian","category":"os","aliases":[]},
			{"name":"ghost","label":"Ghost","category":"app","aliases":[]}]}`,
		"/products/kubernetes": `{"result":{"name":"kubernetes","releases":[
			{"name":"1.30","releaseDate":"2024-04-17","isEol":false,"eolFrom":"2099-06-28","latest":{"name":"1.30.2"}},
			{"name":"1.28","releaseDate":"2023-08-15","isEol":true,"eolFrom":"2024-10-28","latest":{"name":"1.28.15"}}]}}`,
		"/products/postgresql": `{"result":{"name":"postgresql","releases":[
			{"name":"15","releaseDate":"2022-10-13","isEol":false,"eolFrom":"2099-11-11","latest":{"name":"15.19"}}]}}`,
		"/products/redis": `{"result":{"name":"redis","releases":[
			{"name":"7.2","releaseDate":"2023-08-15","isEol":false,"eolFrom":"` + soon + `","latest":{"name":"7.2.11"}},
			{"name":"6.2","releaseDate":"2021-02-22","isEol":true,"eolFrom":"2025-01-01","latest":{"name":"6.2.20"}}]}}`,
		"/products/nginx": `{"result":{"name":"nginx","releases":[
			{"name":"1.28","releaseDate":"2025-04-23","isEol":false,"eolFrom":null,"latest":{"name":"1.28.0"}}]}}`,
		"/products/debian": `{"result":{"name":"debian","releases":[
			{"name":"12","codename":"Bookworm","releaseDate":"2023-06-10","isEol":false,"eolFrom":"2099-06-30","latest":{"name":"12.13"}}]}}`,
		"/products/ghost": `{"result":{"name":"ghost","releases":[
			{"name":"6","releaseDate":"2025-09-01","isEol":false,"eolFrom":null,"latest":{"name":"6.2.0"}}]}}`,
	}
	for _, path := range omit {
		delete(pages, path)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body, ok := pages[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, "<html>not found</html>")
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

const clusterVersionBody = `{"clientVersion":{"gitVersion":"v1.36.1"},"serverVersion":{"gitVersion":"v1.30.2+k3s1"}}`

const clusterNodesBody = `{"items":[
	{"metadata":{"name":"cp-1"},"status":{"nodeInfo":{"kubeletVersion":"v1.30.2+k3s1"}}},
	{"metadata":{"name":"old-2"},"status":{"nodeInfo":{"kubeletVersion":"v1.28.0"}}},
	{"metadata":{"name":"old-1"},"status":{"nodeInfo":{"kubeletVersion":"v1.28.0"}}}]}`

const clusterPodsBody = `{"items":[
	{"metadata":{"namespace":"db","name":"pg-0"},"spec":{"containers":[{"image":"postgres:15.4"}]}},
	{"metadata":{"namespace":"app","name":"api-0"},"spec":{
		"containers":[{"image":"docker.io/library/postgres:15.4"},{"image":"myco/internal-app:2.3.1"}],
		"initContainers":[{"image":"ghcr.io/x/redis:6.2.1-alpine"}]}},
	{"metadata":{"namespace":"app","name":"api-1"},"spec":{"containers":[{"image":"docker.io/library/postgres:15.4"},{"image":"redis:7.2.4"}]}},
	{"metadata":{"namespace":"web","name":"nginx-0"},"spec":{"containers":[{"image":"nginx"}]}},
	{"metadata":{"namespace":"web","name":"edge-0"},"spec":{"containers":[{"image":"nginx:latest-alpine"}]}},
	{"metadata":{"namespace":"web","name":"cache-0"},"spec":{"containers":[{"image":"quay.io/foo/redis@sha256:0123456789abcdef"}]}},
	{"metadata":{"namespace":"os","name":"shell-0"},"spec":{"containers":[{"image":"debian:bookworm-slim"}]}},
	{"metadata":{"namespace":"blog","name":"ghost-0"},"spec":{"containers":[{"image":"ghost:5.9"}]}}]}`

func eolRequest(values map[string]any) plugin.Request {
	for _, c := range Plugin(nil, nil).Capabilities {
		if c.ID == "audit.kube.eol" {
			return plugin.NewRequest(plugin.Resolve(c, plugin.Inputs{Caller: values}), false, false)
		}
	}
	panic("no audit.kube.eol")
}

func TestKubeEOLGradesTheControlPlaneTheKubeletsAndEveryDistinctImage(t *testing.T) {
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody, "pods": clusterPodsBody})
	srv, requests := newEOLServer(t)

	out, err := runKubeEOLAt(t.Context(), eolRequest(nil), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderText(t, out)
	for _, want := range []string{
		"control plane: v1.30.2+k3s1 | " + findings.OK + " | kubernetes 1.30 is supported until 2099-06-28",
		"kubelets at v1.28.0 (2 nodes) | " + findings.Fail + " | kubernetes 1.28 reached end of life on 2024-10-28",
		"nodes: old-1, old-2",
		"kubelets at v1.30.2+k3s1 (1 node) | " + findings.OK,
		// Three pods across two namespaces pull the same image under two
		// spellings of its name: one row, counted once per pod.
		"image postgres:15.4 (3 pods in 2 namespaces) | " + findings.OK + " | postgresql 15 is supported until 2099-11-11",
		// An init container's image is graded like a main one's, and the
		// variant suffix is not part of the release.
		"image redis:6.2.1-alpine (1 pod in 1 namespace) | " + findings.Fail + " | redis 6.2 reached end of life on 2025-01-01",
		"image redis:7.2.4 (1 pod in 1 namespace) | " + findings.Warn + " | redis 7.2 reaches end of life on",
		"image nginx (1 pod in 1 namespace) | " + findings.Info + " | a floating tag names no release",
		// A variant suffix on a floating tag is still a floating tag.
		"image nginx:latest-alpine (1 pod in 1 namespace) | " + findings.Info + " | a floating tag names no release",
		"image redis (1 pod in 1 namespace) | " + findings.Info + " | pinned by digest with no tag",
		"image debian:bookworm-slim (1 pod in 1 namespace) | " + findings.OK + " | debian 12 is supported until 2099-06-30",
		// A product the catalogue lists with no cycle the tag matches.
		"image ghost:5.9 (1 pod in 1 namespace) | " + findings.Info + " | 5.9 names no release cycle of ghost",
		"images endoflife.date does not track (1) | " + findings.Info + " | internal-app",
		"https://endoflife.date/postgresql",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}
	// The catalogue, then one page per distinct product — never per pod or
	// per image: six products in the cluster, seven requests.
	if *requests != 7 {
		t.Errorf("%d requests to the API, want 7 (the catalogue plus one per distinct product)", *requests)
	}
}

func TestKubeEOLNarrowedToANamespaceLeavesTheNodesAloneAndSaysSo(t *testing.T) {
	logPath := fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
		"pods": `{"items":[{"metadata":{"namespace":"db","name":"pg-0"},"spec":{"containers":[{"image":"postgres:15.4"}]}}]}`})
	srv, _ := newEOLServer(t)

	out, err := runKubeEOLAt(t.Context(), eolRequest(map[string]any{"namespace": "db"}), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderText(t, out)
	if !strings.Contains(rendered, "kubelets not examined | "+findings.Info) {
		t.Errorf("a narrowed run did not say the nodes were skipped:\n%s", rendered)
	}
	if strings.Contains(rendered, "kubelets at") {
		t.Errorf("a narrowed run graded kubelets:\n%s", rendered)
	}
	// The namespace count is noise when there is one by construction.
	if !strings.Contains(rendered, "image postgres:15.4 (1 pod) | "+findings.OK) {
		t.Errorf("the image row in a narrowed run:\n%s", rendered)
	}
	got := calls(t, logPath)
	if strings.Contains(got, "nodes") {
		t.Errorf("kubectl was asked for nodes on a narrowed run:\n%s", got)
	}
	if !strings.Contains(got, "--namespace=db") {
		t.Errorf("the pod read was not narrowed:\n%s", got)
	}
}

// The API answering without a kubernetes page is not a cluster with nothing
// to say about its control plane: the rows are there, graded as unknown.
func TestKubeEOLSaysWhenTheAPIHasNoKubernetesPage(t *testing.T) {
	fakeKubectl(t, map[string]string{"version": clusterVersionBody, "nodes": clusterNodesBody,
		"pods": `{"items":[]}`})
	srv, _ := newEOLServer(t, "/products/kubernetes")

	out, err := runKubeEOLAt(t.Context(), eolRequest(nil), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderText(t, out)
	for _, want := range []string{
		"control plane: v1.30.2+k3s1 | " + findings.Info + " | endoflife.date has no release data for kubernetes",
		"kubelets at v1.28.0 (2 nodes) | " + findings.Info + " | endoflife.date has no release data for kubernetes",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("missing %q in:\n%s", want, rendered)
		}
	}
}

func TestKubeEOLReadsTheClusterBeforeAskingTheAPI(t *testing.T) {
	orig := kubectlBin
	kubectlBin = "/nonexistent/kubectl-must-not-run"
	t.Cleanup(func() { kubectlBin = orig })
	srv, requests := newEOLServer(t)

	_, err := runKubeEOLAt(t.Context(), eolRequest(nil), srv.URL)
	if err == nil {
		t.Fatal("no kubectl and the audit answered")
	}
	if *requests != 0 {
		t.Errorf("%d requests reached the API for a cluster that could not be read", *requests)
	}
}

func TestImageRef(t *testing.T) {
	cases := []struct {
		ref, name, tag string
		digest         bool
	}{
		{"postgres:15.4", "postgres", "15.4", false},
		{"docker.io/library/postgres:15.4", "postgres", "15.4", false},
		{"ghcr.io/cloudnative-pg/postgresql:16.4-3", "postgresql", "16.4-3", false},
		{"registry:5000/team/App:v2", "app", "v2", false},
		{"nginx", "nginx", "", false},
		{"quay.io/foo/redis@sha256:0123", "redis", "", true},
		{"redis:7.2.4@sha256:0123", "redis", "7.2.4", true},
	}
	for _, c := range cases {
		name, tag, digest := imageRef(c.ref)
		if name != c.name || tag != c.tag || digest != c.digest {
			t.Errorf("imageRef(%q) = (%q, %q, %v), want (%q, %q, %v)", c.ref, name, tag, digest, c.name, c.tag, c.digest)
		}
	}
}

func TestReleaseOf(t *testing.T) {
	cases := map[string]string{
		"15.4-alpine":       "15.4",
		"v1.35.3+k3s1":      "1.35.3",
		"16.4-3":            "16.4",
		"bookworm-slim":     "bookworm",
		"1.25.3-alpine3.18": "1.25.3",
		"7.2.4-v2":          "7.2.4",
		"latest":            "latest",
		"2.3.1_rc1":         "2.3.1",
	}
	for tag, want := range cases {
		if got := releaseOf(tag); got != want {
			t.Errorf("releaseOf(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestCycleForPrefersTheLongestMatchingCycleThenACodename(t *testing.T) {
	releases := []eolapi.Release{{Name: "1"}, {Name: "1.35"}, {Name: "1.3"}, {Name: "12", Codename: "Bookworm"}}
	cases := map[string]string{
		"1.35.3":   "1.35",
		"1.3.9":    "1.3",
		"1.9.0":    "1",
		"bookworm": "12",
		"12.4":     "12",
		"2.0":      "",
	}
	for version, want := range cases {
		rel, ok := cycleFor(releases, version)
		if (want == "") == ok || rel.Name != want {
			t.Errorf("cycleFor(%q) = %q, %v; want %q", version, rel.Name, ok, want)
		}
	}
}

func TestProductIndexLetsANameWinOverAnotherProductsAlias(t *testing.T) {
	index := productIndex([]eolapi.CatalogueEntry{
		{Name: "postgresql", Aliases: []string{"postgres", "PG"}},
		{Name: "postgres", Aliases: nil},
	})
	if index["postgres"] != "postgres" || index["pg"] != "postgresql" || index["postgresql"] != "postgresql" {
		t.Errorf("index = %v", index)
	}
}

func TestCollectImagesCountsAPodOncePerImageHoweverManyContainersUseIt(t *testing.T) {
	var pods list[podImageItem]
	body := `{"items":[{"metadata":{"namespace":"a","name":"p"},"spec":{
		"containers":[{"image":"redis:7"},{"image":"redis:7"}],
		"initContainers":[{"image":"redis:7"}]}}]}`
	if err := json.Unmarshal([]byte(body), &pods); err != nil {
		t.Fatal(err)
	}
	images := collectImages(pods.Items)
	if len(images) != 1 || images[0].pods != 1 || len(images[0].namespaces) != 1 {
		t.Errorf("images = %+v", images)
	}
}
