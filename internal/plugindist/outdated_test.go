package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Outdated is a label comparison, so it needs no built plugin and no install
// — a locked entry and an index manifest are the whole input, the same
// minimal setup Search's own test uses.
func TestOutdatedComparesRecordedAgainstClaimed(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	if verr := recordInstall(LockEntry{Name: "pg", Digest: "d", Version: "0.1.0",
		Index: "lab", InstalledAt: time.Now()}); verr != nil {
		t.Fatal(verr)
	}

	if rows := Outdated(); len(rows) != 0 {
		t.Fatalf("rows = %+v, want none — the recorded version matches the claim", rows)
	}

	// The index gains a new version of the same plugin.
	bumped := strings.Replace(goodManifest, "version: 0.1.0", "version: 0.2.0", 1)
	writeManifests(t, repo, map[string]string{"pg": bumped})
	commitAll(t, repo, "bump pg")
	if verr := UpdateIndex(context.Background(), "lab"); verr != nil {
		t.Fatal(verr)
	}

	rows := Outdated()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Name != "pg" || rows[0].Index != "lab" ||
		rows[0].InstalledVersion != "0.1.0" || rows[0].AvailableVersion != "0.2.0" ||
		rows[0].Problem != "" {
		t.Fatalf("row = %+v", rows[0])
	}
}

// A manifest the index drops is a row naming why, not a silent skip — the
// same "one bad file costs only itself" rule Manifests applies, here applied
// per installed plugin instead of per file in the index.
func TestOutdatedNamesThePluginTheIndexNoLongerCarries(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest, "redis": named(goodManifest, "redis")})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	if verr := recordInstall(LockEntry{Name: "redis", Digest: "d", Version: "0.1.0",
		Index: "lab", InstalledAt: time.Now()}); verr != nil {
		t.Fatal(verr)
	}

	// The upstream index drops the manifest this plugin was installed from.
	if err := os.Remove(filepath.Join(repo, "index", "redis.yaml")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "drop redis")
	if verr := UpdateIndex(context.Background(), "lab"); verr != nil {
		t.Fatal(verr)
	}

	rows := Outdated()
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want one", rows)
	}
	if rows[0].Name != "redis" || rows[0].Problem == "" || rows[0].AvailableVersion != "" {
		t.Fatalf("row = %+v, want a Problem naming the gap", rows[0])
	}
}
