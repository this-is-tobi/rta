package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Nothing rta ships reaches 1.0.0 until the first stable release is decided
// on purpose, for rta and its plugins at once. Pre-1.0 a breaking change
// moves the minor, which release-please does only with bump-minor-pre-major:
// the plugins' repository had no such line, and its first `fix(s3)!`
// proposed s3 1.0.0 in a release pull request. So both halves are held here:
// the setting on every package, and every version a release pull request
// rewrites — the manifest and the chart's two — whatever took one past 0.x,
// a missing setting or a Release-As footer. In internal/app because the
// prose job runs this package on a release pull request, which changes
// nothing a code job would run for. Lift it in the change that decides the
// stable release.
func TestNothingIsReleasedAsStableBeforeThatIsDecided(t *testing.T) {
	root := repoRoot(t)

	var config struct {
		Packages map[string]map[string]any `json:"packages"`
	}
	readJSON(t, filepath.Join(root, "release-please-config.json"), &config)
	if len(config.Packages) == 0 {
		t.Fatal("release-please-config.json names no package")
	}
	for name, pkg := range config.Packages {
		if pkg["bump-minor-pre-major"] != true {
			t.Errorf("release-please-config.json: package %q needs \"bump-minor-pre-major\": true, "+
				"or a breaking change releases it as 1.0.0", name)
		}
	}

	var manifest map[string]string
	readJSON(t, filepath.Join(root, ".release-please-manifest.json"), &manifest)
	for name, version := range manifest {
		if !preStable(version) {
			t.Errorf(".release-please-manifest.json: %q is at %s, past 0.x", name, version)
		}
	}

	chart, err := os.ReadFile(filepath.Join(root, "charts", "rta-chart", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"version", "appVersion"} {
		m := regexp.MustCompile(`(?m)^` + key + `:\s*"?([^"\s]+)"?\s*$`).FindSubmatch(chart)
		if m == nil {
			t.Errorf("Chart.yaml has no %s line", key)
			continue
		}
		if !preStable(string(m[1])) {
			t.Errorf("Chart.yaml: %s is %s, past 0.x", key, m[1])
		}
	}
}

func preStable(version string) bool { return strings.HasPrefix(version, "0.") }

func readJSON(t *testing.T, path string, into any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
