package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// knownIndexIsLocal points the one known name at a repository on this
// machine: what is under test is name resolution and the reservation, not a
// network clone.
func knownIndexIsLocal(t *testing.T) string {
	t.Helper()
	repo := gitFixture(t, map[string]string{"pg": goodManifest})
	prev := knownIndexes
	knownIndexes = map[string]string{"official": repo}
	t.Cleanup(func() { knownIndexes = prev })
	return repo
}

func TestAKnownIndexIsAttachedByNameAlone(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	repo := knownIndexIsLocal(t)

	if verr := AddIndex(context.Background(), "official", ""); verr != nil {
		t.Fatalf("attaching the known index by name: %v", verr)
	}
	ix, ok := IndexByName("official")
	if !ok {
		t.Fatal("official is not attached")
	}
	origin, verr := IndexOrigin(context.Background(), ix)
	if verr != nil {
		t.Fatal(verr)
	}
	if origin != repo {
		t.Errorf("origin = %q, want the known repository %q", origin, repo)
	}
}

func TestTheKnownNameAcceptsItsOwnURLToo(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	repo := knownIndexIsLocal(t)

	if verr := AddIndex(context.Background(), "official", repo); verr != nil {
		t.Fatalf("the reserved name refused the very repository it is reserved for: %v", verr)
	}
}

func TestTheOfficialNameCannotPointElsewhere(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	knownIndexIsLocal(t)
	other := gitFixture(t, map[string]string{"pg": goodManifest})

	verr := AddIndex(context.Background(), "official", other)
	if verr == nil {
		t.Fatal("a reserved name was attached to a different repository")
	}
	if verr.Code != "plugin.index.reserved" {
		t.Errorf("code = %q, want plugin.index.reserved", verr.Code)
	}
	if _, ok := IndexByName("official"); ok {
		t.Error("the refusal left an index behind")
	}
}

func TestAnUnknownNameNeedsARepository(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	knownIndexIsLocal(t)

	verr := AddIndex(context.Background(), "mine", "")
	if verr == nil {
		t.Fatal("an unknown name with no repository was attached")
	}
	if verr.Code != "plugin.index.url" {
		t.Errorf("code = %q, want plugin.index.url", verr.Code)
	}
	if !strings.Contains(verr.Hint, "rta plugin index add official") {
		t.Errorf("the hint should name the one index rta knows; got %q", verr.Hint)
	}
}

func TestNothingAttachedNamesTheOneCommandThatFixesIt(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())

	_, verr := Resolve("pg")
	if verr == nil {
		t.Fatal("resolving with no index attached succeeded")
	}
	if verr.Code != "plugin.index.none" {
		t.Errorf("code = %q, want plugin.index.none", verr.Code)
	}
	if !strings.Contains(verr.Hint, "rta plugin index add official") {
		t.Errorf("hint = %q, want it to name `rta plugin index add official`", verr.Hint)
	}
	if verr = UpdateIndex(context.Background(), ""); verr == nil || !strings.Contains(verr.Hint, "rta plugin index add official") {
		t.Errorf("index update with nothing attached: %v", verr)
	}
}

// `rta pg` was told it meant "pkg", `rta docker` that it meant "doctor" and
// `rta qdrant` that it meant "grant", and the plugin hint was given to a
// service or withheld from it by how near the word lay to a command. The
// names are static so the answer does not depend on the neighbours.
func TestAFirstPartyPluginIsKnownByName(t *testing.T) {
	for _, name := range []string{"pg", "mysql", "mariadb", "etcd", "qdrant", "redis", "s3", "vault",
		"kube", "cnpg", "docker", "keycloak"} {
		got, ok := FirstParty(name)
		if !ok || got != name {
			t.Errorf("FirstParty(%q) = %q, %v", name, got, ok)
		}
		if hint := FirstPartyHint(name); !strings.Contains(hint, "`rta plugin install "+name+"`") ||
			!strings.Contains(hint, name+" is a first-party plugin") {
			t.Errorf("FirstPartyHint(%q) = %q", name, hint)
		}
	}
}

func TestTheLongSpellingOfAFirstPartyServiceNamesIt(t *testing.T) {
	for word, want := range map[string]string{"postgres": "pg", "postgresql": "pg", "k8s": "kube",
		"kubernetes": "kube", "PG": "pg"} {
		got, ok := FirstParty(word)
		if !ok || got != want {
			t.Errorf("FirstParty(%q) = %q, %v, want %q", word, got, ok, want)
		}
	}
	if hint := FirstPartyHint("k8s"); !strings.Contains(hint, "k8s is the first-party plugin kube") ||
		!strings.Contains(hint, "`rta plugin install kube`") {
		t.Errorf("an alias was answered with %q", hint)
	}
}

// A word that merely resembles a first-party name, or a service rta has no
// plugin for, is not told it is one: the hint is exact or absent.
func TestAWordNoPluginAnswersToIsNotTold(t *testing.T) {
	for _, word := range []string{"", "pk", "pkg", "mongo", "helm", "install", "doctor", "grant", "kv", "rds",
		"official"} {
		if name, ok := FirstParty(word); ok {
			t.Errorf("FirstParty(%q) = %q", word, name)
		}
		if hint := FirstPartyHint(word); hint != "" {
			t.Errorf("FirstPartyHint(%q) = %q", word, hint)
		}
	}
}

// The list is typed in and the docs are typed by hand, which is two places a
// thirteenth plugin can be added to and one forgotten. The docs link each
// plugin to its directory in rta-plugins, and that link is what is read.
func TestTheFirstPartyNamesAreTheOnesTheDocsList(t *testing.T) {
	root := repoRootOf(t)
	link := regexp.MustCompile(`\| \[` + "`" + `([a-z0-9]+)` + "`" + `\]\(https://github\.com/this-is-tobi/rta-plugins/tree/main/plugins/([a-z0-9]+)\)`)
	documented := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "docs"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range link.FindAllStringSubmatch(string(body), -1) {
			if m[1] == m[2] {
				documented[m[1]] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var docs []string
	for name := range documented {
		docs = append(docs, name)
	}
	slices.Sort(docs)
	if got := FirstPartyNames(); !slices.Equal(got, docs) {
		t.Errorf("rta knows the first-party plugins %v and the docs list %v", got, docs)
	}
}

func repoRootOf(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's working directory")
		}
		dir = parent
	}
}
