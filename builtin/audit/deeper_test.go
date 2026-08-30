package audit

import (
	"strings"
	"testing"
)

// **A handoff has to fit what was read, or nobody reads the next one.**
//
// The rows exist because this plugin has a ceiling — it grades what a project
// already committed and never resolves, crawls or syncs a vulnerability
// database — and a report that hits its ceiling quietly reads as an
// all-clear. A row naming knip to a Go project is the failure mode of the
// fix: a reader who has seen one row that does not apply to them stops
// reading the ones that do.
func TestTheDeeperRowsFitWhatWasActuallyRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		manifests []string
		want      []string
		notWant   []string
	}{
		{"go", []string{"go.mod"},
			[]string{"govulncheck", "go mod tidy", "reachability"},
			[]string{"knip", "pnpm", "cargo", "pip-audit"}},
		{"pnpm", []string{"pnpm-lock.yaml"},
			[]string{"`pnpm audit`", "`knip`"},
			// Backticked, because "npm audit" is a substring of "pnpm audit"
			// and a loose assertion here passes for the wrong reason.
			[]string{"`npm audit`", "govulncheck", "reachability"}},
		{"npm", []string{"package-lock.json"},
			[]string{"`npm audit`", "`knip`"},
			[]string{"`pnpm audit`", "govulncheck"}},
		{"rust", []string{"Cargo.lock"},
			[]string{"cargo audit", "cargo machete"},
			[]string{"knip", "govulncheck"}},
		{"python", []string{"uv.lock"},
			[]string{"pip-audit", "deptry"},
			[]string{"knip", "govulncheck"}},
		{"polyglot", []string{"services/api/go.mod", "apps/web/pnpm-lock.yaml"},
			[]string{"govulncheck", "`pnpm audit`", "`knip`", "go mod tidy"},
			[]string{"`npm audit`", "cargo audit"}},
		// An SBOM names no package manager, so only the rows that need none
		// have anything to say.
		{"sbom only", []string{"bom.json"},
			[]string{"trivy fs", "syft"},
			[]string{"govulncheck", "knip", "`cargo audit`", "`npm audit`"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			for _, p := range depsDeeper(".", false, tc.manifests) {
				sb.WriteString(p.Key + " " + p.Value + "\n")
			}
			got := sb.String()
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("no row mentions %q:\n%s", w, got)
				}
			}
			for _, n := range tc.notWant {
				if strings.Contains(got, n) {
					t.Errorf("a row mentions %q, which this project has no use for:\n%s", n, got)
				}
			}
		})
	}
}

// A tool named once per manifest is a tool named eleven times in a monorepo.
func TestADeeperCommandIsNamedOncePerRepositoryNotOncePerFile(t *testing.T) {
	many := []string{"a/go.mod", "b/go.mod", "c/go.mod", "d/pnpm-lock.yaml", "e/pnpm-lock.yaml"}
	got := pickCommands(many, nativeAudit)
	want := []string{"govulncheck ./...", "pnpm audit"}
	if len(got) != len(want) {
		t.Fatalf("five manifests produced %v, want one command per tool: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("commands = %v, want %v (sorted, so the row reads the same twice running)", got, want)
		}
	}
}

// A repository read over the network has no path to hand anybody, so a
// command pointed at one has to be a command that would actually run. trivy
// is the only one of these that takes a URL; the rest have to say they run in
// a checkout, rather than being printed with a URL where a directory goes.
func TestARemoteAuditNeverPrintsACommandThatWouldFail(t *testing.T) {
	const url = "https://github.com/org/repo"
	var sb strings.Builder
	for _, p := range depsDeeper(url, true, []string{"go.mod"}) {
		sb.WriteString(p.Key + " " + p.Value + "\n")
	}
	got := sb.String()
	if !strings.Contains(got, "trivy repo "+url) {
		t.Errorf("the one tool that takes a URL was not given it:\n%s", got)
	}
	for _, wrong := range []string{"trivy fs " + url, "grype dir:" + url, "syft " + url} {
		if strings.Contains(got, wrong) {
			t.Errorf("printed %q, which is a command that would fail", wrong)
		}
	}
}
