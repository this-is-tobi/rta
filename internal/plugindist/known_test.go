package plugindist

import (
	"context"
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
