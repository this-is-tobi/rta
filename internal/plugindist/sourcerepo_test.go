package plugindist

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The mistake this file is about, in the order it happened: a plugin's own
// source repository was attached as an index, because that is where the
// plugins are. It cloned, it reported success, `plugin index list` showed it
// with "0 plugins, 0 problems", and `plugin search vault` answered "nothing
// matches" — a sentence about the plugin somebody searched for, said by a
// command that had nothing to search.
//
// Every check below is one link of that chain.

// sourceRepoFixture is a repository shaped like a plugin's source tree: a
// plugins/ directory holding a directory per plugin. git records no empty
// directory, so each carries a file — which is what makes the fixture the real
// shape rather than an approximation of it.
func sourceRepoFixture(t *testing.T, plugins ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on $PATH")
	}
	dir := t.TempDir()
	for _, p := range plugins {
		sub := filepath.Join(dir, "plugins", p)
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sub, "main.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		base := []string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t",
			"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}
		if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "--quiet")
	run("add", ".")
	run("commit", "--quiet", "-m", "source")
	return dir
}

// placeIndex builds an index directory under rta's data dir without going
// through AddIndex. Some of the states below are ones AddIndex now refuses to
// create — they are still reachable, because `plugin index update` pulls
// whatever the upstream became.
func placeIndex(t *testing.T, name string, manifests map[string]string) Index {
	t.Helper()
	dir := filepath.Join(indexesDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, doc := range manifests {
		if err := os.WriteFile(filepath.Join(dir, "plugins", n+".yaml"), []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Index{Name: name, Dir: dir}
}

func TestAPluginsSourceRepositoryIsRefusedAsAnIndex(t *testing.T) {
	testData(t)
	repo := sourceRepoFixture(t, "cnpg", "kube", "vault")

	verr := AddIndex(context.Background(), "official", repo)
	if verr == nil {
		t.Fatal("a repository of plugin source attached as an index")
	}
	if verr.Code != "plugin.index.empty" {
		t.Fatalf("code = %q, want plugin.index.empty", verr.Code)
	}
	// The count is the part that tells an operator which mistake they made:
	// a directory per plugin is a source tree, and no entries at all is an
	// index somebody has not filled in yet.
	if !strings.Contains(verr.Message, "3 directories") {
		t.Fatalf("message does not say what it found instead: %q", verr.Message)
	}
	if !strings.Contains(verr.Hint, "nothing was attached") {
		t.Fatalf("hint does not say the clone is gone: %q", verr.Hint)
	}
}

// A refused attach leaves nothing behind, the rule the failed clone beside it
// already follows: absence is the one state every other command reads
// correctly, and a half-attached index is what every command downstream would
// then be answering from.
func TestARefusedAttachLeavesNoIndexBehind(t *testing.T) {
	testData(t)
	repo := sourceRepoFixture(t, "vault")

	if verr := AddIndex(context.Background(), "official", repo); verr == nil {
		t.Fatal("want a refusal")
	}
	if got := Indexes(); len(got) != 0 {
		t.Fatalf("attached indexes after a refusal = %v", got)
	}
	if _, err := os.Stat(filepath.Join(indexesDir(), "official")); !os.IsNotExist(err) {
		t.Fatalf("the clone is still on disk: %v", err)
	}
}

// Refusing the shape must not refuse the thing itself.
func TestARealIndexStillAttaches(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": goodManifest})

	if verr := AddIndex(context.Background(), "lab", repo); verr != nil {
		t.Fatalf("a sound index was refused: %v", verr)
	}
	if got := Indexes(); len(got) != 1 || got[0].Name != "lab" {
		t.Fatalf("indexes = %v", got)
	}
}

// One malformed manifest costs the operator that manifest. Every manifest
// malformed costs them the index, because there is nothing left to attach —
// and the refusal is the parser's own, not a second opinion about it.
func TestAnIndexWhoseEveryManifestIsMalformedIsNotAttached(t *testing.T) {
	testData(t)
	repo := gitFixture(t, map[string]string{"pg": "this: is: not: a manifest\n"})

	verr := AddIndex(context.Background(), "lab", repo)
	if verr == nil {
		t.Fatal("want a refusal")
	}
	if verr.Code == "plugin.index.empty" {
		t.Fatalf("the parser's own reason was replaced by a generic one: %v", verr)
	}
	if !strings.Contains(verr.Message, "pg.yaml") {
		t.Fatalf("the refusal does not name the file: %q", verr.Message)
	}
}

// The check is on what plugins/ holds, not on whether it exists. Those read
// the same for a directory of manifests and differently for everything else,
// which is the whole defect.
func TestAnEmptyPluginsDirectoryIsNotAnEmptyCatalogue(t *testing.T) {
	testData(t)
	ix := placeIndex(t, "hollow", nil)

	listed, bad := Manifests(ix)
	if len(listed) != 0 {
		t.Fatalf("listed = %v", listed)
	}
	if len(bad) != 1 || bad[0].Code != "plugin.index.empty" {
		t.Fatalf("problems = %v, want one plugin.index.empty", bad)
	}
}

// Search is where somebody actually asks, so it is where the reason has to
// surface. "nothing matches" is a claim about a catalogue; with no readable
// index there was no catalogue to make it about.
func TestSearchReportsTheIndexItCouldNotRead(t *testing.T) {
	testData(t)
	placeIndex(t, "official", nil)

	rows, bad := Search("vault", "")
	if len(rows) != 0 {
		t.Fatalf("rows = %v", rows)
	}
	if len(bad) != 1 || !strings.Contains(bad[0].Message, "official") {
		t.Fatalf("problems = %v, want the index named", bad)
	}
}

// A broken index must not cost the operator the ones that work — and must not
// be invisible behind them either. Both halves, because dropping the problems
// is exactly how the first one used to be bought.
func TestASoundIndexAnswersBesideABrokenOne(t *testing.T) {
	testData(t)
	placeIndex(t, "lab", map[string]string{"pg": goodManifest})
	placeIndex(t, "official", nil)

	rows, bad := Search("", "")
	if len(rows) != 1 || rows[0].Name != "pg" || rows[0].Index != "lab" {
		t.Fatalf("rows = %v, want the sound index's claim", rows)
	}
	if len(bad) != 1 || !strings.Contains(bad[0].Message, "official") {
		t.Fatalf("problems = %v, want the broken index named", bad)
	}
}
