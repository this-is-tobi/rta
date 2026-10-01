package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/builtin/internal/pathin"
	"github.com/this-is-tobi/rta/internal/pathguard"
	"github.com/this-is-tobi/rta/pkg/findings"
	"github.com/this-is-tobi/rta/pkg/plugin"
	"github.com/this-is-tobi/rta/pkg/view"
)

// A manifest under a root that is another name for one of rta's own files —
// a hard link to grants.key called package-lock.json — is withheld by the
// call's bounds, and the scan says so. Taken for "not there" like any other
// failed stat, a directory holding nothing else was answered "no lockfile
// or SBOM", and one holding more was audited as though the rest were all it
// declared.
func TestAManifestTheBoundsWithholdIsNamed(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	t.Setenv("RTA_DATA_DIR", data)
	t.Setenv("RTA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
	key := filepath.Join(data, "grants.key")
	only := filepath.Join(root, "only")
	both := filepath.Join(root, "both")
	for _, dir := range []string{data, only, both} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(key, []byte("seal"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(both, "go.mod"), []byte("module example.com/m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{only, both} {
		if err := os.Link(key, filepath.Join(dir, "package-lock.json")); err != nil {
			t.Skipf("hard links unavailable here: %v", err)
		}
	}
	g, err := pathguard.New(root)
	if err != nil {
		t.Fatal(err)
	}
	bounded := func(values map[string]any) plugin.Request {
		return req(values).WithSurface(plugin.SurfaceMCP).WithConfinement(g.Derived).WithBounds(g.Bounds())
	}

	_, err = runDeps(t.Context(), bounded(map[string]any{"path": only, "offline": true}))
	if verr := view.AsError(err, "x"); verr == nil || verr.Code != "core.mcp.path.protected" {
		t.Errorf("audit.deps of a directory whose one lockfile is withheld: %v, want the bounds' refusal", err)
	}
	_, err = runWhy(t.Context(), bounded(map[string]any{"package": "lodash", "path": only}))
	if verr := view.AsError(err, "x"); verr == nil || verr.Code != "core.mcp.path.protected" {
		t.Errorf("audit.why of a directory whose one lockfile is withheld: %v, want the bounds' refusal", err)
	}

	names, cov, err := findManifests(pathin.FS(bounded(map[string]any{}), both), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "go.mod" {
		t.Errorf("found %v, want go.mod alone", names)
	}
	if len(cov.withheld) != 1 || cov.withheld[0].name != "package-lock.json" ||
		cov.withheld[0].err.Code != "core.mcp.path.protected" {
		t.Errorf("withheld %+v, want package-lock.json with the bounds' refusal", cov.withheld)
	}

	r := &findings.Report{}
	addCoverage(r, cov)
	f := mustFind(t, r, "scan")
	if f.Status != findings.Warn || !strings.Contains(f.Detail, "package-lock.json") {
		t.Errorf("the scan finding is %q %q, want a warning naming package-lock.json", f.Status, f.Detail)
	}
}
