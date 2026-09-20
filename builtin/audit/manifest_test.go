package audit

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/findings"
)

// monorepoWithAnUnreadableService builds the arrangement the walk has to
// survive honestly: two services, one of them a directory this process
// cannot list, holding a manifest nobody will get to read.
//
// A root-owned directory left by a prior container run, a bind mount with
// restrictive ACLs, a submodule checked out by another UID — all ordinary
// on a CI runner or a shared box, and none of them a reason for the report
// to describe the rest of the tree as the whole of it.
func monorepoWithAnUnreadableService(t *testing.T) (dir string, blind string) {
	t.Helper()
	dir = t.TempDir()
	readable := filepath.Join(dir, "services", "a")
	if err := os.MkdirAll(readable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readable, "go.mod"),
		[]byte("module example.com/a\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blind = filepath.Join(dir, "services", "b")
	if err := os.MkdirAll(blind, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blind, "package-lock.json"),
		[]byte(`{"lockfileVersion":3,"packages":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blind, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blind, 0o755) })
	return dir, blind
}

// **A subtree the scan could not read is not a subtree with nothing in it.**
//
// Skipping it and carrying on is right — eleven readable directories are
// not abandoned for one that is not — but the caller was never told, and
// `truncated` only ever fires for the maxManifests/maxScanDepth bounds. So
// a monorepo with one unreadable service produced a report that named the
// manifests it found, counted them, graded them, and read exactly like a
// complete dependency audit of the whole repository.
func TestARecursiveScanReportsTheDirectoriesItCouldNotRead(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("file modes do not deny the owner here")
	}
	dir, _ := monorepoWithAnUnreadableService(t)

	found, cov, err := findManifests(os.DirFS(dir), true)
	if err != nil {
		t.Fatal(err)
	}
	// The readable half still answers: skipping is not abandoning.
	if !slices.Contains(found, "services/a/go.mod") {
		t.Errorf("found = %v, want the readable service's go.mod", found)
	}
	if slices.Contains(found, "services/b/package-lock.json") {
		t.Errorf("found = %v — a manifest inside an unreadable directory cannot have been read", found)
	}
	if !slices.Contains(cov.unreadable, "services/b") {
		t.Fatalf("unreadable = %v, want the directory the walk could not list — "+
			"without it the report reads as a complete audit of the repository", cov.unreadable)
	}
}

// And an ordinary tree reports nothing, so the caveat stays worth reading.
func TestAReadableScanReportsNothingUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"),
		[]byte("module example.com/x\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found, cov, err := findManifests(os.DirFS(dir), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || len(cov.unreadable) != 0 || cov.truncated {
		t.Errorf("found=%v coverage=%+v, want one manifest and no caveats", found, cov)
	}
}

// The caveat reaches the report as a finding, beside the bounds caveat it
// sits next to — a Warn, because a dependency audit that covered part of
// the tree and says "no known vulnerabilities" is the wrong kind of quiet.
func TestUnreadableDirectoriesBecomeAFinding(t *testing.T) {
	r := &findings.Report{}
	addCoverage(r, coverage{unreadable: []string{"services/b", "vendor/x"}})
	var detail, status string
	for _, f := range r.Findings {
		if f.Check == "scan" {
			detail, status = f.Detail, f.Status
		}
	}
	if status != findings.Warn {
		t.Fatalf("an unread subtree graded %q, want %q", status, findings.Warn)
	}
	for _, want := range []string{"services/b", "vendor/x"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not name %s", detail, want)
		}
	}
}
