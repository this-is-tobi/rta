package audit

import (
	"context"
	"encoding/json"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseGoMod(t *testing.T) {
	got := parseGoMod(`module github.com/example/thing

go 1.26.0

require (
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.53.0 // indirect
)

require github.com/single/dep v0.1.0

replace github.com/example/other => ../other

exclude github.com/bad/one v6.6.6
`, "go.mod")

	want := map[string]string{
		"github.com/spf13/cobra": "v1.10.2",
		"golang.org/x/crypto":    "v0.53.0",
		"github.com/single/dep":  "v0.1.0",
	}
	if len(got) != len(want) {
		t.Fatalf("read %d requirements, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		if c.ecosystem != "Go" {
			t.Errorf("%s: ecosystem %q, want Go", c.name, c.ecosystem)
		}
		if want[c.name] != c.version {
			t.Errorf("%s: version %q, want %q", c.name, c.version, want[c.name])
		}
	}
	// replace and exclude name versions that are not what the build uses;
	// reporting them would put a version the project does not run into a
	// security report.
	for _, c := range got {
		if strings.Contains(c.name, "bad/one") || strings.Contains(c.name, "example/other") {
			t.Errorf("a replace/exclude directive was read as a requirement: %+v", c)
		}
	}
}

func TestParsePackageLock(t *testing.T) {
	v3 := []byte(`{
	  "lockfileVersion": 3,
	  "packages": {
	    "": {"name": "root", "version": "1.0.0"},
	    "node_modules/lodash": {"version": "4.17.21"},
	    "node_modules/@scope/pkg": {"version": "2.0.0"},
	    "node_modules/a/node_modules/nested": {"version": "0.1.0"},
	    "node_modules/noversion": {}
	  }
	}`)
	got, err := parsePackageLock(v3, "package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, c := range got {
		if c.ecosystem != "npm" {
			t.Errorf("%s: ecosystem %q", c.name, c.ecosystem)
		}
		found[c.name] = c.version
	}
	for name, version := range map[string]string{
		"lodash": "4.17.21", "@scope/pkg": "2.0.0", "nested": "0.1.0",
	} {
		if found[name] != version {
			t.Errorf("%s: got %q, want %q (all: %v)", name, found[name], version, found)
		}
	}
	if _, ok := found["root"]; ok {
		t.Error("the root project was reported as a dependency of itself")
	}
	if _, ok := found["noversion"]; ok {
		t.Error("a package with no version was reported")
	}

	// v1 lockfiles are still in the wild and use a different shape entirely.
	v1 := []byte(`{"lockfileVersion": 1, "dependencies": {"minimist": {"version": "1.2.5"}}}`)
	got, err = parsePackageLock(v1, "package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].name != "minimist" || got[0].version != "1.2.5" {
		t.Errorf("v1 lockfile not read: %+v", got)
	}
}

// A range does not name a version. Guessing which one is installed would put
// a version the file never stated into a security report.
func TestParseRequirementsTakesOnlyPinnedVersions(t *testing.T) {
	got := parseRequirements(`# comment
django==4.2.1
requests>=2.0        # a range, not a pin
celery[redis]==5.3.0
flask==2.0.0 ; python_version < "3.11"
urllib3 == 1.26.5
-r other.txt
--hash=sha256:abcdef

`, "requirements.txt")

	want := map[string]string{
		"django": "4.2.1", "celery": "5.3.0", "flask": "2.0.0", "urllib3": "1.26.5",
	}
	if len(got) != len(want) {
		t.Fatalf("read %d pins, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		if c.ecosystem != "PyPI" {
			t.Errorf("%s: ecosystem %q, want PyPI", c.name, c.ecosystem)
		}
		if want[c.name] != c.version {
			t.Errorf("%s: version %q, want %q", c.name, c.version, want[c.name])
		}
	}
}

func TestParseCycloneDXAndSPDX(t *testing.T) {
	cdx := []byte(`{
	  "bomFormat": "CycloneDX", "specVersion": "1.5",
	  "components": [
	    {"name": "lodash", "version": "4.17.21", "purl": "pkg:npm/lodash@4.17.21"},
	    {"name": "guava", "version": "31.0", "purl": "pkg:maven/com.google.guava/guava@31.0"},
	    {"name": "mystery", "version": "1.0"}
	  ]
	}`)
	got, err := parseSBOM(cdx, "bom.json")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]component{}
	for _, c := range got {
		by[c.name] = c
	}
	if c := by["lodash"]; c.ecosystem != "npm" || c.version != "4.17.21" {
		t.Errorf("npm component wrong: %+v", c)
	}
	// Maven identifies a package as group:artifact, unlike every other
	// ecosystem with a namespace.
	if c := by["com.google.guava:guava"]; c.ecosystem != "Maven" || c.version != "31.0" {
		t.Errorf("maven coordinates not joined: %+v (all: %v)", c, by)
	}
	// No purl means no ecosystem, which is reported as unchecked rather than
	// guessed at.
	if c := by["mystery"]; c.ecosystem != "" || c.version != "1.0" {
		t.Errorf("a component with no purl was given an ecosystem: %+v", c)
	}

	spdx := []byte(`{
	  "spdxVersion": "SPDX-2.3",
	  "packages": [
	    {"name": "requests", "versionInfo": "2.31.0", "externalRefs": [
	      {"referenceType": "purl", "referenceLocator": "pkg:pypi/requests@2.31.0"}]},
	    {"name": "bare", "versionInfo": "9.9"}
	  ]
	}`)
	got, err = parseSBOM(spdx, "sbom.spdx.json")
	if err != nil {
		t.Fatal(err)
	}
	by = map[string]component{}
	for _, c := range got {
		by[c.name] = c
	}
	if c := by["requests"]; c.ecosystem != "PyPI" || c.version != "2.31.0" {
		t.Errorf("spdx purl not read: %+v", c)
	}
	if c := by["bare"]; c.ecosystem != "" {
		t.Errorf("a package with no purl was given an ecosystem: %+v", c)
	}

	// A JSON file that is not an SBOM must not be read as an empty one and
	// reported as "no dependencies".
	other, err := parseSBOM([]byte(`{"name": "tsconfig", "compilerOptions": {}}`), "x.json")
	if err != nil || other != nil {
		t.Errorf("unrelated JSON parsed as an SBOM: %+v, %v", other, err)
	}
}

// OSV's ecosystem names are exact and case-sensitive, and a wrong one returns
// no vulnerabilities rather than an error — the worst possible failure mode
// for a security check.
func TestPURLEcosystemsUseOSVSpelling(t *testing.T) {
	cases := map[string]struct{ eco, name, version string }{
		"pkg:golang/golang.org/x/crypto@v0.53.0": {"Go", "golang.org/x/crypto", "v0.53.0"},
		"pkg:npm/%40scope/pkg@1.0.0":             {"npm", "%40scope/pkg", "1.0.0"},
		"pkg:pypi/django@4.2":                    {"PyPI", "django", "4.2"},
		"pkg:cargo/serde@1.0":                    {"crates.io", "serde", "1.0"},
		"pkg:gem/rails@7.0":                      {"RubyGems", "rails", "7.0"},
		"pkg:nuget/Newtonsoft.Json@13.0":         {"NuGet", "Newtonsoft.Json", "13.0"},
		"pkg:npm/lodash@4.17.21?arch=x64":        {"npm", "lodash", "4.17.21"},
		"pkg:npm/lodash@4.17.21#sub/path":        {"npm", "lodash", "4.17.21"},
	}
	for purl, want := range cases {
		got, ok := fromPURL(purl, "src")
		if !ok {
			t.Errorf("%s: not parsed", purl)
			continue
		}
		if got.ecosystem != want.eco || got.name != want.name || got.version != want.version {
			t.Errorf("%s: got %+v, want %+v", purl, got, want)
		}
	}
	// Malformed and unversioned purls are refused rather than half-read.
	for _, bad := range []string{"", "lodash@1.0", "pkg:", "pkg:npm", "pkg:npm/lodash", "pkg:npm/@1.0"} {
		if got, ok := fromPURL(bad, "src"); ok && got.version != "" && got.name != "" {
			t.Errorf("fromPURL(%q) returned %+v", bad, got)
		}
	}
}

func TestDedupeCollapsesTheSamePackageFromTwoManifests(t *testing.T) {
	in := []component{
		{ecosystem: "npm", name: "lodash", version: "4.17.21", source: "bom.json"},
		{ecosystem: "npm", name: "lodash", version: "4.17.21", source: "package-lock.json"},
		{ecosystem: "npm", name: "lodash", version: "4.17.20", source: "package-lock.json"},
		{ecosystem: "npm", name: "", version: "1.0"},
		{ecosystem: "npm", name: "x", version: ""},
	}
	got := dedupe(in)
	if len(got) != 2 {
		t.Fatalf("dedupe gave %d components, want 2: %+v", len(got), got)
	}
	// A different version of the same package is a different component: both
	// may be installed, and both may be vulnerable.
	if got[0].version == got[1].version {
		t.Errorf("two versions collapsed into one: %+v", got)
	}
}

func TestFindManifests(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"go.mod", "package-lock.json", "README.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err := findManifests(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("found %v, want go.mod and package-lock.json", got)
	}
	// A file may be named directly, whatever it is called.
	one := filepath.Join(dir, "README.md")
	if got, _, err := findManifests(one, false); err != nil || len(got) != 1 || got[0] != one {
		t.Errorf("naming a file directly: %v, %v", got, err)
	}
	if _, _, err := findManifests(filepath.Join(dir, "nope"), false); err == nil {
		t.Error("a missing path should be an error")
	}
}

// A monorepo keeps its manifests one level down, and the default scan is
// deliberately not recursive — so without --recursive the whole repository
// reads as having no dependencies at all, which is the one wrong answer this
// capability must never give.
func TestFindManifestsWalksAMonorepoOnlyWhenAsked(t *testing.T) {
	root := t.TempDir()
	write := func(rel string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("services/api/go.mod")
	write("services/worker/uv.lock")
	write("apps/web/pnpm-lock.yaml")
	// The three traps: a vendored tree, a build output, and a dot directory.
	write("apps/web/node_modules/lodash/package-lock.json")
	write("services/api/vendor/x/go.mod")
	write(".git/modules/thing/go.mod")

	flat, _, err := findManifests(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(flat) != 0 {
		t.Errorf("the default scan must not walk: %v", flat)
	}

	deep, truncated, err := findManifests(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("six manifests is not a truncating scan")
	}
	want := map[string]bool{"go.mod": true, "uv.lock": true, "pnpm-lock.yaml": true}
	if len(deep) != len(want) {
		t.Fatalf("found %v, want exactly one manifest per package", deep)
	}
	for _, p := range deep {
		if !want[filepath.Base(p)] {
			t.Errorf("%s should not have been reached", p)
		}
		if strings.Contains(p, "node_modules") || strings.Contains(p, "vendor") ||
			strings.Contains(p, string(filepath.Separator)+".") {
			t.Errorf("descended somewhere it must not: %s", p)
		}
	}
}

// Results come back positionally. A short answer that got matched up anyway
// would attribute one package's vulnerabilities to another, which is worse
// than reporting nothing.
func TestOSVRefusesAMismatchedResponse(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_, _ = io.WriteString(w, `{"results":[{"vulns":[{"id":"GHSA-1"}]}]}`)
	}))
	defer srv.Close()

	comps := []component{
		{ecosystem: "npm", name: "a", version: "1"},
		{ecosystem: "npm", name: "b", version: "2"},
	}
	_, err := queryOSVAt(context.Background(), srv.Client(), srv.URL, comps)
	if err == nil {
		t.Fatal("a response with fewer results than queries was accepted")
	}
	if !strings.Contains(err.Error(), "positional") {
		t.Errorf("the error should say why it cannot be matched: %v", err)
	}
}

func TestOSVMapsVulnerabilitiesToTheirPackage(t *testing.T) {
	var seen map[string]any
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		_ = json.NewDecoder(r.Body).Decode(&seen)
		_, _ = io.WriteString(w, `{"results":[{"vulns":[]},{"vulns":[{"id":"GHSA-2"},{"id":"GHSA-3"}]}]}`)
	}))
	defer srv.Close()

	comps := []component{
		{ecosystem: "Go", name: "clean", version: "v1"},
		{ecosystem: "Go", name: "affected", version: "v2"},
	}
	got, err := queryOSVAt(context.Background(), srv.Client(), srv.URL, comps)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[comps[0].key()]) != 0 {
		t.Errorf("vulnerabilities attributed to the clean package: %v", got)
	}
	if ids := got[comps[1].key()]; len(ids) != 2 || ids[0] != "GHSA-2" {
		t.Errorf("affected package got %v, want both advisories", ids)
	}
	// The request has to speak OSV's schema, or it answers "no vulnerabilities"
	// for everything and the whole capability quietly reports all-clear.
	queries, _ := seen["queries"].([]any)
	if len(queries) != 2 {
		t.Fatalf("request body was not a queries array: %v", seen)
	}
	first, _ := queries[0].(map[string]any)
	pkg, _ := first["package"].(map[string]any)
	if pkg["ecosystem"] != "Go" || pkg["name"] != "clean" || first["version"] != "v1" {
		t.Errorf("query shape wrong: %v", first)
	}
}

func TestOSVReportsAnErrorStatus(t *testing.T) {
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusTooManyRequests)
	}))
	defer srv.Close()
	_, err := queryOSVAt(context.Background(), srv.Client(), srv.URL,
		[]component{{ecosystem: "npm", name: "a", version: "1"}})
	if err == nil {
		t.Fatal("a 429 was treated as a clean result")
	}
}

func TestDepsGradesAffectedPackages(t *testing.T) {
	comps := []component{
		{ecosystem: "Go", name: "example.com/clean", version: "v1.0.0"},
		{ecosystem: "Go", name: "example.com/bad", version: "v0.1.0"},
	}
	vulns := map[string][]string{comps[1].key(): {"GHSA-zzz", "GHSA-aaa"}}

	r := &report{}
	gradeDeps(r, comps, comps, nil, nil, []string{"go.mod"}, vulns, false)

	f := mustFind(t, r, "example.com/bad")
	if f.status != stFail {
		t.Errorf("an affected package graded %q", f.status)
	}
	// Sorted, so the same run reads the same way twice.
	if !strings.Contains(f.detail, "GHSA-aaa, GHSA-zzz") {
		t.Errorf("advisory list not sorted: %q", f.detail)
	}
	if !strings.Contains(f.detail, "https://osv.dev/vulnerability/") {
		t.Errorf("the finding should be followable: %q", f.detail)
	}
	if _, ok := find(r, "example.com/clean"); ok {
		t.Error("a package with no advisories got a finding of its own")
	}
	// A hit has to name what goes deeper — that is ADR 0008's first rule.
	next := mustFind(t, r, "next step")
	if !strings.Contains(next.detail, "trivy") {
		t.Errorf("no scanner named for the depth this does not have: %q", next.detail)
	}
}

func TestDepsSaysWhatItCouldNotCheck(t *testing.T) {
	known := []component{{ecosystem: "Go", name: "a", version: "v1"}}
	unknown := []component{{name: "mystery", version: "1.0"}}

	r := &report{}
	gradeDeps(r, append(known, unknown...), known, unknown,
		[]unreadableManifest{{path: "weird.json", reason: "invalid character"}},
		[]string{"go.mod", "weird.json"}, nil, false)

	// Silent partial coverage is the failure mode that matters: a report that
	// looks complete and is not.
	unchecked := mustFind(t, r, "unchecked")
	if unchecked.status != stWarn || !strings.Contains(unchecked.detail, "mystery") {
		t.Errorf("unrecognised components not declared: %+v", unchecked)
	}
	bad := mustFind(t, r, "manifest")
	if bad.status != stWarn || !strings.Contains(bad.detail, "weird.json") {
		t.Errorf("an unparseable manifest was not declared: %+v", bad)
	}
}

func TestDepsOfflineDoesNotClaimAnAllClear(t *testing.T) {
	comps := []component{{ecosystem: "Go", name: "a", version: "v1"}}
	r := &report{}
	gradeDeps(r, comps, comps, nil, nil, []string{"go.mod"}, nil, true)

	f := mustFind(t, r, "advisories")
	if f.status != stInfo {
		t.Errorf("offline graded %q — it must not read as a clean bill of health", f.status)
	}
	if status, _ := r.worst(); status != stOK {
		t.Errorf("an offline inventory graded the project %q", status)
	}
	if !strings.Contains(f.detail, "--offline") {
		t.Errorf("the finding should say why nothing was checked: %q", f.detail)
	}
}

func TestEveryDepsFindingLandsInADeclaredGroup(t *testing.T) {
	declared := map[string]bool{}
	for _, g := range depsGroupOrder {
		declared[g] = true
	}
	comps := []component{{ecosystem: "Go", name: "a", version: "v1"}, {name: "b", version: "2"}}
	for _, offline := range []bool{true, false} {
		r := &report{}
		gradeDeps(r, comps, comps[:1], comps[1:],
			[]unreadableManifest{{path: "x.json", reason: "unexpected end of JSON input"}},
			[]string{"go.mod"}, map[string][]string{comps[0].key(): {"GHSA-1"}}, offline)
		for _, f := range r.findings {
			if !declared[f.group] {
				t.Errorf("finding %q is in group %q, which the detail page never renders", f.check, f.group)
			}
			if f.ref.cwe == "" {
				t.Errorf("finding %q cites no control", f.check)
			}
		}
	}
}
