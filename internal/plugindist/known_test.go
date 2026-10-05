package plugindist

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/pkg/plugin"
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
		if hint := FirstPartyHint(name, nil); !strings.Contains(hint, "`rta plugin install "+name+"`") ||
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
	if hint := FirstPartyHint("k8s", nil); !strings.Contains(hint, "k8s is the first-party plugin kube") ||
		!strings.Contains(hint, "`rta plugin install kube`") {
		t.Errorf("an alias was answered with %q", hint)
	}
}

// `rta explain pg.query`, `rta dashboard add pg.query` and `rta grant allow
// pg.query` each said the capability was unknown and left the person to work
// out that the plugin was not installed; the namespace names it.
func TestACapabilityOfAFirstPartyPluginNamesThePlugin(t *testing.T) {
	for target, want := range map[string]string{
		"pg.query": "pg is a first-party plugin", "pg": "pg is a first-party plugin",
		"kube.pod.list": "kube is a first-party plugin", "k8s.pod.list": "k8s is the first-party plugin kube",
	} {
		if hint := FirstPartyHintFor(target, nil); !strings.Contains(hint, want) {
			t.Errorf("FirstPartyHintFor(%q) = %q, want %q", target, hint, want)
		}
	}
	for _, target := range []string{"", "kv.get", "mongo.find", "pgx.query", ".pg"} {
		if hint := FirstPartyHintFor(target, nil); hint != "" {
			t.Errorf("FirstPartyHintFor(%q) = %q, want none", target, hint)
		}
	}
}

// The words that tell a person to install a plugin are wrong about one that is
// already running: `explain pg.qury` with pg installed is a typo, `rta
// postgres` is a way of asking for `rta pg`, and neither is answered with the
// install. Said of the name the word resolves to, so an alias is covered.
func TestAPluginThatIsRunningIsNotTold(t *testing.T) {
	pg := []plugin.Capability{{ID: "pg.status"}, {ID: "pg.query"}}
	for _, target := range []string{"pg.qury", "pg", "postgres.query", "pg.query"} {
		if hint := FirstPartyHintFor(target, pg); hint != "" {
			t.Errorf("FirstPartyHintFor(%q) with pg running = %q, want none", target, hint)
		}
	}
	if hint := FirstPartyHintFor("redis.get", pg); !strings.Contains(hint, "`rta plugin install redis`") {
		t.Errorf("another plugin's capability was answered with %q, want its install", hint)
	}
	if hint := FirstPartyHint("k8s", func(name string) bool { return name == "kube" }); hint != "" {
		t.Errorf("FirstPartyHint(k8s) with kube running = %q, want none", hint)
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
		if hint := FirstPartyHint(word, nil); hint != "" {
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
