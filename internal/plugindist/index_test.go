package plugindist

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// gitFixture makes a real repository holding the given plugins/<name>.yaml
// files. Real git rather than a directory pretending, because AddIndex is a
// clone and UpdateIndex is a pull — the operations under test are git's, so
// the fixture must be something git recognises.
func gitFixture(t *testing.T, manifests map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on $PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}
		out, err := exec.Command("git", append(base, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--quiet")
	writeManifests(t, dir, manifests)
	run("add", ".")
	run("commit", "--quiet", "-m", "manifests")
	return dir
}

func writeManifests(t *testing.T, repo string, manifests map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(repo, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, doc := range manifests {
		if err := os.WriteFile(filepath.Join(repo, "plugins", name+".yaml"),
			[]byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func commitAll(t *testing.T, repo, msg string) {
	t.Helper()
	base := []string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t",
		"-c", "commit.gpgsign=false"}
	for _, args := range [][]string{{"add", "."}, {"commit", "--quiet", "-m", msg}} {
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func testData(t *testing.T) {
	t.Helper()
	t.Setenv("RTA_DATA_DIR", t.TempDir())
}

func named(doc, name string) string {
	return strings.ReplaceAll(doc, "pg", name)
}

// The index lifecycle end to end: attach clones, the catalogue reads,
// update pulls what the upstream gained, and detach removes — unless the
// lockfile still names the index as provenance.
func TestAnIndexIsAttachedUpdatedAndDetached(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	ctx := context.Background()

	if verr := AddIndex(ctx, "lab", repo); verr != nil {
		t.Fatalf("add: %v", verr)
	}
	ix, ok := IndexByName("lab")
	if !ok {
		t.Fatal("the attached index is not listed")
	}
	listed, bad := Manifests(ix)
	if len(bad) != 0 || len(listed) != 1 || listed[0].Manifest.Name != "pg" {
		t.Fatalf("manifests = %v, bad = %v", listed, bad)
	}

	// The upstream gains a plugin; update makes it visible.
	writeManifests(t, repo, map[string]string{"redis": named(goodManifest, "redis")})
	commitAll(t, repo, "add redis")
	if verr := UpdateIndex(ctx, "lab"); verr != nil {
		t.Fatalf("update: %v", verr)
	}
	listed, _ = Manifests(ix)
	if len(listed) != 2 {
		t.Fatalf("after update, manifests = %v", listed)
	}

	// Provenance holds the index in place until the plugin is gone.
	if verr := recordInstall(LockEntry{Name: "pg", Digest: "d", Index: "lab",
		InstalledAt: time.Now()}); verr != nil {
		t.Fatal(verr)
	}
	if verr := RemoveIndex("lab"); verr == nil || verr.Code != "plugin.index.held" {
		t.Fatalf("removing a referenced index: %v, want plugin.index.held", verr)
	}
	if verr := recordRemoval("pg"); verr != nil {
		t.Fatal(verr)
	}
	if verr := RemoveIndex("lab"); verr != nil {
		t.Fatalf("remove: %v", verr)
	}
	if _, still := IndexByName("lab"); still {
		t.Fatal("the index is still attached after remove")
	}
}

func TestAttachRefusals(t *testing.T) {
	testData(t)
	ctx := context.Background()
	if verr := AddIndex(ctx, "Bad_Name", "x"); verr == nil || verr.Code != "plugin.index.name" {
		t.Fatalf("verr = %v", verr)
	}
	if verr := AddIndex(ctx, "lab", "--upload-pack=evil"); verr == nil ||
		verr.Code != "plugin.index.url" {
		t.Fatalf("an option-shaped url: %v, want the argv refusal", verr)
	}
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	if verr := AddIndex(ctx, "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	if verr := AddIndex(ctx, "lab", repo); verr == nil || verr.Code != "plugin.index.exists" {
		t.Fatalf("attaching twice: %v", verr)
	}
	// A clone that fails leaves nothing half-attached.
	if verr := AddIndex(ctx, "gone", filepath.Join(t.TempDir(), "absent")); verr == nil {
		t.Fatal("cloning nothing succeeded")
	}
	if _, exists := IndexByName("gone"); exists {
		t.Fatal("a failed clone left a half-attached index")
	}
}

// One malformed manifest is reported and costs nothing else; and a manifest
// whose declared name disagrees with its filename is refused, because the
// file's placement is the index's claim, and a claim is not an identity.
func TestABadManifestCostsOnlyItself(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{
		"pg":       goodManifest,
		"broken":   "name: [",
		"imposter": named(goodManifest, "kv"), // declares kv, sits at imposter.yaml
	})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	ix, _ := IndexByName("lab")
	listed, bad := Manifests(ix)
	if len(listed) != 1 || listed[0].Manifest.Name != "pg" {
		t.Fatalf("listed = %v", listed)
	}
	if len(bad) != 2 {
		t.Fatalf("bad = %v, want the parse failure and the name disagreement", bad)
	}
	names := bad[0].Message + " " + bad[1].Message
	if !strings.Contains(names, "imposter") || !strings.Contains(names, "the file's name is the claim") {
		t.Fatalf("refusals = %q", names)
	}
}

func TestResolveRefusesAmbiguityAndHonoursQualification(t *testing.T) {
	testData(t)
	ctx := context.Background()
	for _, name := range []string{"alpha", "beta"} {
		repo := gitFixture(t, map[string]string{"pg": goodManifest})
		if verr := AddIndex(ctx, name, repo); verr != nil {
			t.Fatal(verr)
		}
	}
	if _, verr := Resolve("pg"); verr == nil || verr.Code != "plugin.install.ambiguous" ||
		!strings.Contains(verr.Hint, "alpha/pg") || !strings.Contains(verr.Hint, "beta/pg") {
		t.Fatalf("two indexes, bare name: %v", verr)
	}
	got, verr := Resolve("beta/pg")
	if verr != nil || got.Index != "beta" {
		t.Fatalf("qualified resolve = %+v, %v", got, verr)
	}
	if _, verr := Resolve("nope"); verr == nil || verr.Code != "plugin.install.unknown" {
		t.Fatalf("unknown plugin: %v", verr)
	}
	if _, verr := Resolve("gamma/pg"); verr == nil || verr.Code != "plugin.index.unknown" {
		t.Fatalf("unknown index: %v", verr)
	}
}

// Search answers from claims alone — nothing is fetched — and labels each row
// with the index making the claim.
func TestSearchAnswersFromClaims(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{
		"pg":    goodManifest,
		"redis": named(goodManifest, "redis"),
	})
	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatal(verr)
	}
	rows := Search("", "")
	if len(rows) != 2 || rows[0].Name != "pg" || rows[1].Name != "redis" {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0].Index != "lab" || rows[0].Version != "0.1.0" {
		t.Fatalf("row = %+v", rows[0])
	}
	if got := Search("redis", ""); len(got) != 1 || got[0].Name != "redis" {
		t.Fatalf("term filter = %v", got)
	}
	if got := Search("", "write"); len(got) != 2 {
		t.Fatalf("safety filter (both claim a write) = %v", got)
	}
	if got := Search("", "destructive"); len(got) != 0 {
		t.Fatalf("safety filter = %v, want none destructive", got)
	}
}
