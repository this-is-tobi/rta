package plugindist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/this-is-tobi/rta/internal/plugintrust"
)

// stored puts one version of a plugin in the store the way place does,
// without an install: a directory named by digest holding the binary.
func stored(t *testing.T, name, digest, content string) string {
	t.Helper()
	dir := filepath.Join(StoreDir(), name, digest)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, binaryName(name))
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// current points bin/ at one stored version, the relative link place writes.
func current(t *testing.T, name, digest string) {
	t.Helper()
	if err := os.MkdirAll(BinDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join("..", "store", name, digest, binaryName(name))
	if err := os.Symlink(target, filepath.Join(BinDir(), binaryName(name))); err != nil {
		t.Fatal(err)
	}
}

// An upgrade keeps the previous artifact and nothing ever took the older
// ones out. Pruning keeps what runs — the version bin/ points at, which is
// the one rta.lock records — and withdraws trust from the rest, the way
// remove does; the preview touches nothing.
func TestPruneKeepsWhatRunsAndWithdrawsTrustFromTheRest(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	older, old, cur := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	for _, d := range []string{older, old, cur} {
		path := stored(t, "hello", d, "version "+d[:1])
		if verr := plugintrust.Add(d, "hello", path); verr != nil {
			t.Fatal(verr)
		}
	}
	current(t, "hello", cur)
	if verr := recordInstall(LockEntry{Name: "hello", Digest: cur}); verr != nil {
		t.Fatal(verr)
	}

	preview, verr := PreviewPrune()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(preview) != 1 || preview[0].Name != "hello" ||
		strings.Join(preview[0].Removed, " ") != older+" "+old ||
		strings.Join(preview[0].Kept, " ") != cur || preview[0].Bytes == 0 {
		t.Fatalf("preview = %+v, want the two older versions named and the current one kept", preview)
	}
	if got := StoredDigests("hello"); len(got) != 3 {
		t.Fatalf("the preview removed something: %v", got)
	}
	if !plugintrust.Load().Trusts(old) {
		t.Fatal("the preview withdrew trust")
	}

	pruned, verr := Prune()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(pruned) != 1 || strings.Join(pruned[0].Removed, " ") != older+" "+old {
		t.Fatalf("pruned = %+v", pruned)
	}
	if got := StoredDigests("hello"); strings.Join(got, " ") != cur {
		t.Errorf("store holds %v, want the current version alone", got)
	}
	set := plugintrust.Load()
	if set.Trusts(older) || set.Trusts(old) {
		t.Error("trust was left standing for bytes that are gone")
	}
	if !set.Trusts(cur) {
		t.Error("trust was withdrawn from the version that runs")
	}
	if l, ok := LockedFor("hello"); !ok || l.Digest != cur {
		t.Errorf("lock = %+v, want the current version still recorded", l)
	}

	again, verr := Prune()
	if verr != nil || len(again) != 0 {
		t.Errorf("a second prune found %+v, want nothing", again)
	}
}

// A plugin whose store names no current version — no bin/ link and no lock
// record — is left as it is and said so, because nothing here may guess
// which copy somebody runs.
func TestPruneLeavesAPluginWithNoCurrentVersionAlone(t *testing.T) {
	t.Setenv("RTA_DATA_DIR", t.TempDir())
	t.Setenv("RTA_SYSTEM_DIR", "")
	d := strings.Repeat("d", 64)
	stored(t, "orphan", d, "version")

	pruned, verr := Prune()
	if verr != nil {
		t.Fatal(verr)
	}
	if len(pruned) != 1 || !pruned[0].Unclear || len(pruned[0].Removed) != 0 {
		t.Fatalf("pruned = %+v, want the plugin reported as unclear and untouched", pruned)
	}
	if got := StoredDigests("orphan"); strings.Join(got, " ") != d {
		t.Errorf("store holds %v, want the one version still there", got)
	}
}
