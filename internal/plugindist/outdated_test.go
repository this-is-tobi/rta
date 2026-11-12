package plugindist

import (
	"context"
	"fmt"
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
		Index: "lab", InstalledAt: time.Now()}, nil); verr != nil {
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

// A claim below the installed version by its first three numbers is marked as
// one; a claim above it, and a version that does not begin with three numbers,
// are not.
func TestOutdatedMarksAClaimOlderThanWhatIsInstalled(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest, "redis": named(goodManifest, "redis"),
		"etcd": named(goodManifest, "etcd")})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	for name, installed := range map[string]string{"pg": "0.2.0", "redis": "0.0.9", "etcd": "dev"} {
		if verr := recordInstall(LockEntry{Name: name, Digest: "d", Version: installed,
			Index: "lab", InstalledAt: time.Now()}, nil); verr != nil {
			t.Fatal(verr)
		}
	}
	older := map[string]bool{}
	for _, r := range Outdated() {
		older[r.Name] = r.Older
	}
	if want := map[string]bool{"pg": true, "redis": false, "etcd": false}; fmt.Sprint(older) != fmt.Sprint(want) {
		t.Errorf("older = %v, want %v", older, want)
	}
}

// Two claims are ordered by the three numbers they begin with, a "v" before
// them allowed, and a suffix is never ordered: the same three numbers with one
// on either side, and a claim that does not begin with three numbers, are ones
// rta cannot order. Mirrored, every pair orders the other way.
func TestAVersionClaimIsOrderedByItsFirstThreeNumbers(t *testing.T) {
	for _, tc := range []struct {
		a, b  string
		order int
		ok    bool
	}{
		{"1.3.2", "1.4.0", -1, true},
		{"v1.4.0", "1.4.0", 0, true},
		{"1.10.0", "1.9.9", 1, true},
		{"0.16.3-2-gabc1234-dirty", "0.16.4", -1, true},
		{"2026.11.1", "2026.10.30", 1, true},
		{"01.2.3", "1.2.3", 0, true},
		{strings.Repeat("9", 40) + ".0.0", strings.Repeat("9", 39) + ".0.0", 1, true},
		{"v1.2.0-3-gabc1234", "v1.2.0", 0, false},
		{"v1.2.0-9-gaaaaaaa", "v1.2.0-10-gbbbbbbb", 0, false},
		{"1.4.0-rc.1", "1.4.0", 0, false},
		{"1.4.0+build.5", "1.4.0", 0, false},
		{"dev", "1.0.0", 0, false},
		{"", "1.0.0", 0, false},
		{"1.2", "1.0.0", 0, false},
		{"V1.2.3", "1.0.0", 0, false},
		{"nightly-2026-11-01", "1.0.0", 0, false},
	} {
		order, ok := compareClaims(tc.a, tc.b)
		if order != tc.order || ok != tc.ok {
			t.Errorf("compareClaims(%q, %q) = %d, %v; want %d, %v", tc.a, tc.b, order, ok, tc.order, tc.ok)
		}
		if back, backOK := compareClaims(tc.b, tc.a); back != -order || backOK != ok {
			t.Errorf("compareClaims(%q, %q) = %d, %v; want the mirror of %d, %v", tc.b, tc.a, back, backOK, order, ok)
		}
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
		Index: "lab", InstalledAt: time.Now()}, nil); verr != nil {
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
