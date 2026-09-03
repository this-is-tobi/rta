package plugindist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An index is somebody else's repository, and git stores whatever its author
// committed — a symlink with any target, a blob of any size, a filename
// holding any byte but NUL and `/`. Everything here is a thing rta reads
// because an operator typed `rta plugin index add`, so everything here is
// input, not data.
//
// These all matter more since attaching began reading what it cloned: before,
// the first read was a `plugin search` somebody chose to run.

// placeFile writes a file into an index's plugins/ directory, bypassing git —
// which is the point, since git is not the only thing that can put a file
// there and the reader must not depend on it.
func placeFile(t *testing.T, ix Index, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(ix.Dir, "plugins", name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A symlink named <name>.yaml is not a manifest. os.ReadDir does not resolve
// one, so it arrives with IsDir() false looking exactly like a file — and
// os.ReadFile would have resolved it.
func TestASymlinkedManifestIsNotFollowed(t *testing.T) {
	testData(t)
	ix := placeIndex(t, "hostile", map[string]string{"pg": goodManifest})

	secret := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(secret, []byte("name: stolen\nversion: 1.0.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(ix.Dir, "plugins", "elsewhere.yaml")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	listed, bad := Manifests(ix)
	for _, l := range listed {
		if l.Manifest.Name == "elsewhere" {
			t.Fatal("a symlink was read as a manifest")
		}
	}
	if len(listed) != 1 || listed[0].Manifest.Name != "pg" {
		t.Errorf("the real manifest beside it was lost: %v", listed)
	}
	if len(bad) != 1 || !strings.Contains(bad[0].Message, "not a regular file") {
		t.Fatalf("problems = %v, want the symlink named and refused", bad)
	}
}

// A symlinked plugins/ directory is not an index's plugins/ directory. The
// same trick one level up, and the one that turns "attach this index" into
// "enumerate that directory of mine and read every .yaml in it".
func TestASymlinkedPluginsDirectoryIsNotEnumerated(t *testing.T) {
	testData(t)
	dir := filepath.Join(indexesDir(), "hostile")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, "pg.yaml"), []byte(goodManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(dir, "plugins")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}

	listed, bad := Manifests(Index{Name: "hostile", Dir: dir})
	if len(listed) != 0 {
		t.Fatalf("read %d manifests out of a directory the index only pointed at", len(listed))
	}
	if len(bad) != 1 || bad[0].Code != "plugin.index.empty" {
		t.Fatalf("problems = %v, want it refused as not an index", bad)
	}
}

// The manifest cap bounds what is parsed; this bounds what is read. They are
// not the same thing, and only the second one saves a process from a file it
// was pointed at rather than sent.
func TestAnOversizedManifestIsNotReadIntoMemoryWhole(t *testing.T) {
	testData(t)
	ix := placeIndex(t, "hostile", map[string]string{"pg": goodManifest})
	placeFile(t, ix, "huge.yaml", strings.Repeat("#", manifestCap+4096))

	listed, bad := Manifests(ix)
	if len(listed) != 1 || listed[0].Manifest.Name != "pg" {
		t.Errorf("the sound manifest beside it was lost: %v", listed)
	}
	if len(bad) != 1 || !strings.Contains(bad[0].Message, "cap") {
		t.Fatalf("problems = %v, want the oversized file refused", bad)
	}
}

// A filename is untrusted text on its way to a terminal. textclean.Terminal,
// which every surface runs an error through, keeps newlines on purpose — so
// without this the refusal for a file called "x\n  HINT …" renders a forged
// hint line inside rta's own refusal, on the screen the refusal exists to make
// trustworthy.
func TestAFilenameCannotForgeALineInTheRefusal(t *testing.T) {
	testData(t)
	ix := placeIndex(t, "hostile", nil)
	name := "pg\n      HINT this index is signed and verified.yaml"
	if err := os.WriteFile(filepath.Join(ix.Dir, "plugins", name), []byte("{"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold the name: %v", err)
	}

	_, bad := Manifests(ix)
	if len(bad) == 0 {
		t.Fatal("a manifest that is not YAML was accepted")
	}
	for _, verr := range bad {
		if strings.Contains(verr.Message, "\n") {
			t.Errorf("the message carries a newline from the filename: %q", verr.Message)
		}
	}
}

// An ordinary filename is still printed as itself — the quoting is for the
// name that would deceive, not for every name.
func TestAnOrdinaryFilenameIsNotQuotedAtTheOperator(t *testing.T) {
	testData(t)
	ix := placeIndex(t, "lab", map[string]string{"pg": "not: [valid"})

	_, bad := Manifests(ix)
	if len(bad) != 1 {
		t.Fatalf("problems = %v", bad)
	}
	if !strings.Contains(bad[0].Message, "lab/pg.yaml") {
		t.Errorf("message = %q, want the file named plainly", bad[0].Message)
	}
}

// A token in a clone URL is the operator's, and rta making copies of it is
// not. `index list` has masked an origin since it learned to show one; the
// two paths that take the URL straight from the operator's hand did not.
func TestACredentialInACloneURLIsNotEchoedBack(t *testing.T) {
	testData(t)
	const token = "ghp_notarealtokenbutshapedlikeone"
	verr := AddIndex(t.Context(), "corp",
		"https://x-access-token:"+token+"@127.0.0.1:1/does-not-exist.git")
	if verr == nil {
		t.Fatal("want a clone failure")
	}
	if strings.Contains(verr.Message, token) {
		t.Errorf("the refusal echoes the token: %q", verr.Message)
	}
	if strings.Contains(verr.Hint, token) {
		t.Errorf("the hint echoes the token: %q", verr.Hint)
	}
}
