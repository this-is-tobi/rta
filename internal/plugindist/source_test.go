package plugindist

import (
	"os"
	"path/filepath"
	"testing"
)

// Where a plugin's bytes came from is read off the path of the artifact, and a
// directory that merely begins with the store's name is not the store: the
// store is a directory, and `<store>-old/pg` sits beside it, not in it.
func TestSourceOfTellsTheStoreFromEveryoneElse(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	store := StoreDir()

	for path, want := range map[string]string{
		"":                                     SourceBuiltIn,
		filepath.Join(store, "pg", "abc", "x"): SourceInstalled,
		store + "-old/pg/abc/x":                SourcePath,
		store:                                  SourcePath,
		"/usr/local/bin/rta-plugin-pg":         SourcePath,
	} {
		if got := SourceOf(path); got != want {
			t.Errorf("SourceOf(%q) = %q, want %q", path, got, want)
		}
	}
	if InStore("") {
		t.Error("a plugin compiled into the binary has no path, and is in no store")
	}
}

// An image installs its plugins into a root it reads and never writes, and
// that root is not a $PATH: `rta plugin list` said "found on $PATH" for every
// plugin of the full image, where `rta doctor` said the image installed them.
// It is not the operator's own store either — the question `rta doctor` asks to
// spot a shadowed managed copy has to stay about that store alone, since a copy
// in the root outranks it.
func TestAPluginInTheSystemRootIsNeitherInstalledByRtaNorOnThePath(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	root := t.TempDir()
	t.Setenv("RTA_SYSTEM_DIR", root)

	stored := filepath.Join(root, "plugins", "store", "pg", "abc", "rta-plugin-pg")
	binned := filepath.Join(root, "plugins", "bin", "rta-plugin-kube")
	for path, want := range map[string]string{
		stored: SourceSystem,
		binned: SourceSystem,
		filepath.Join(StoreDir(), "pg", "abc", "x"): SourceInstalled,
		root + "-old/plugins/store/pg/abc/x":        SourcePath,
		root:                                        SourcePath,
	} {
		if got := SourceOf(path); got != want {
			t.Errorf("SourceOf(%q) = %q, want %q", path, got, want)
		}
	}
	if InStore(stored) {
		t.Error("a plugin of the system root is in the operator's store: doctor would stop reporting it as a shadow")
	}
	if !InSystemRoot(stored) || InSystemRoot(filepath.Join(StoreDir(), "pg", "abc", "x")) {
		t.Error("InSystemRoot does not tell the root's plugins from the operator's own")
	}

	t.Setenv("RTA_SYSTEM_DIR", "")
	if got := SourceOf(stored); got != SourcePath || InSystemRoot(stored) {
		t.Errorf("with no system root, %q is %q: nothing is in a root that is not read", stored, got)
	}
}

// pluginhost.Identify hands back the file's real path, and a directory is what
// RTA_DATA_DIR, XDG_DATA_HOME or the home directory said — through /tmp or /var
// on a Mac, or a ~/.local that a dotfiles manager keeps elsewhere. Compared as
// written, every plugin rta had installed was found on $PATH, and doctor warned
// that the managed copy was shadowed by the very path it runs from.
func TestAStoreReachedThroughASymlinkIsStillTheStore(t *testing.T) {
	// Resolved, as the path Identify reports is, since the temporary directory is
	// itself reached through a link on a Mac.
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RTA_DATA_DIR", filepath.Join(link, "data"))
	t.Setenv("RTA_SYSTEM_DIR", filepath.Join(link, "system"))

	installed := filepath.Join(real, "data", "plugins", "store", "pg", "abc", "rta-plugin-pg")
	imaged := filepath.Join(real, "system", "plugins", "store", "pg", "abc", "rta-plugin-pg")
	for path, want := range map[string]string{
		installed: SourceInstalled,
		imaged:    SourceSystem,
		filepath.Join(real, "data-old", "plugins", "store", "pg", "abc", "x"): SourcePath,
	} {
		if got := SourceOf(path); got != want {
			t.Errorf("SourceOf(%q) = %q, want %q", path, got, want)
		}
	}
	if !InStore(installed) {
		t.Errorf("InStore(%q) is false for a store named through %s", installed, link)
	}
}
