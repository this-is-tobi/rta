//go:build !windows

package fs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/this-is-tobi/rta/pkg/view"
)

// A size is the disk a file takes, as du counts it. A sparse file, a VM image
// or a database with holes, was its whole length: a 100 GiB image written with
// a few megabytes was ranked the biggest thing in the tree, above what filled
// the disk.
func TestUsageCountsASparseFileByTheDiskItTakes(t *testing.T) {
	root := t.TempDir()
	const length = 64 << 20
	sparse := filepath.Join(root, "sparse.img")
	if err := os.WriteFile(sparse, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(sparse, length); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(sparse)
	if err != nil {
		t.Fatal(err)
	}
	if disk, _, _ := diskUsage(info); disk >= length/2 {
		t.Skipf("this filesystem allocates a truncated file in full (%d of %d bytes)", disk, length)
	}
	if err := os.WriteFile(filepath.Join(root, "real.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	tbl := run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	if first := tbl.Rows[0][0]; first != "real.bin" {
		t.Errorf("biggest = %q, want real.bin: the sparse file takes a few blocks, whatever its length", first)
	}
	apparent := run(t, runUsage, map[string]any{"path": root, "limit": 20, "apparent": true}).(view.Table)
	if first := apparent.Rows[0][0]; first != "sparse.img" || apparent.Rows[0][1] != "64.0 MiB" {
		t.Errorf("by length the biggest = %v, want sparse.img at 64.0 MiB", apparent.Rows[0])
	}
}

// A file with several names is counted once, where it is first met, as du
// counts it. pnpm's node_modules, an overlay's layers and a backup tool's
// snapshots are mostly such files, and each name was counted in full.
func TestUsageCountsAFileWithSeveralNamesOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "data.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.bin", "two.bin"} {
		if err := os.Link(filepath.Join(root, "a", "data.bin"), filepath.Join(root, "b", name)); err != nil {
			t.Skipf("no hard links here: %v", err)
		}
	}

	tbl := run(t, runUsage, map[string]any{"path": root, "limit": 20}).(view.Table)
	a, b := rowFor(t, tbl, "a/"), rowFor(t, tbl, "b/")
	if a[1] == "0 B" || b[1] != "0 B" {
		t.Errorf("a/ = %q and b/ = %q, want the file counted in a/, where it is first met, and not again in b/", a[1], b[1])
	}
	if a[3] != "1" || b[3] != "2" {
		t.Errorf("files: a/ = %s and b/ = %s, want every name counted as a file", a[3], b[3])
	}
	both := run(t, runUsage, map[string]any{"path": root, "limit": 20, "apparent": true}).(view.Table)
	if a, b := rowFor(t, both, "a/"), rowFor(t, both, "b/"); a[1] != "1.0 MiB" || b[1] != "2.0 MiB" {
		t.Errorf("by length a/ = %q and b/ = %q, want each name in full", a[1], b[1])
	}
}
