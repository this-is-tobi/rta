package plugindist

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The two things a first-time publisher does after `rta plugin manifest
// --index ./my-index` is write a manifest into a plain directory and attach
// it. git answers the first with "repository does not exist", about a path
// that is right there, and the second, a repository with nothing committed,
// clones as an empty one and was refused as an index with no index/ directory
// though index/weather.yaml is on disk. Each is said as what it is, with the
// commands that fix it.

// indexDirectory is a directory holding what `plugin manifest --index` writes,
// and nothing git knows about it.
func indexDirectory(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on $PATH")
	}
	dir := filepath.Join(t.TempDir(), "my-index")
	writeManifests(t, dir, map[string]string{"pg": goodManifest})
	return dir
}

func TestAPlainDirectoryIsSaidToBeNoRepository(t *testing.T) {
	testData(t)
	dir := indexDirectory(t)

	verr := AddIndex(context.Background(), "mine", dir)
	if verr == nil {
		t.Fatal("a directory that is not a repository was attached")
	}
	if verr.Code != "plugin.index.norepo" || !strings.Contains(verr.Message, "not a git repository") {
		t.Errorf("refused as %s: %s", verr.Code, verr.Message)
	}
	if !strings.Contains(verr.Hint, "git init && git add index && git commit") {
		t.Errorf("the hint does not give the commands that make it one: %q", verr.Hint)
	}
	if got := Indexes(); len(got) != 0 {
		t.Errorf("attached after a refusal: %v", got)
	}
}

// A preview makes the same check, since it needs no network: it did not, and
// said "would attach" about a directory the real run refused.
func TestAPreviewRefusesAPlainDirectoryToo(t *testing.T) {
	testData(t)
	dir := indexDirectory(t)

	if verr := PreviewAddIndex(context.Background(), "mine", dir); verr == nil || verr.Code != "plugin.index.norepo" {
		t.Fatalf("a preview of a plain directory answered %v", verr)
	}
}

// A directory inside somebody's project is not a repository of its own either,
// and git says the same of it as of a directory in no repository at all.
func TestADirectoryInsideAnotherRepositoryIsNoRepositoryEither(t *testing.T) {
	testData(t)
	outer := sourceRepoFixture(t, "pg")
	dir := filepath.Join(outer, "my-index")
	writeManifests(t, dir, map[string]string{"pg": goodManifest})

	verr := AddIndex(context.Background(), "mine", dir)
	if verr == nil || verr.Code != "plugin.index.norepo" {
		t.Fatalf("a directory inside a repository was answered %v", verr)
	}
}

func TestARepositoryWithNothingCommittedIsSaidToHaveNoCommits(t *testing.T) {
	testData(t)
	dir := indexDirectory(t)
	if out, err := exec.Command("git", "-C", dir, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	verr := AddIndex(context.Background(), "mine", dir)
	if verr == nil {
		t.Fatal("a repository with no commit was attached")
	}
	if verr.Code != "plugin.index.empty" || !strings.Contains(verr.Message, "no commits") {
		t.Errorf("refused as %s: %s", verr.Code, verr.Message)
	}
	if !strings.Contains(verr.Hint, "git add index && git commit") || !strings.Contains(verr.Hint, "nothing was attached") {
		t.Errorf("the hint does not say what to commit, or that nothing was attached: %q", verr.Hint)
	}
	if got := Indexes(); len(got) != 0 {
		t.Errorf("attached after a refusal: %v", got)
	}
}

// A path that is nothing at all is git's to describe, and a repository that
// cannot be read for another reason keeps git's own words: the new sentences
// are for the two states they are true of.
func TestAPathThatDoesNotExistKeepsGitsAnswer(t *testing.T) {
	testData(t)
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git on $PATH")
	}

	verr := AddIndex(context.Background(), "mine", filepath.Join(t.TempDir(), "nope"))
	if verr == nil || verr.Code != "plugin.index.add" {
		t.Fatalf("a path that does not exist was answered %v", verr)
	}
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if verr := AddIndex(context.Background(), "mine", file); verr == nil || verr.Code != "plugin.index.add" {
		t.Fatalf("a file was answered %v", verr)
	}
}
